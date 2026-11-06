// Command icongen 画出应用图标：一块纯色圆角方，中间一个白色的宋体「译」。
//
//	go run ./tools/icongen                 # 写 resources/icon.png
//	go run ./tools/icongen -o /tmp/i.png   # 换个地方看效果
//	go run ./tools/icongen -bg '#1f6feb'   # 换底色
//
// 所有形状都在 4 倍画布上画完再缩回 1024，圆角与笔画边缘都是干净的。
// 字体用系统的 Songti SC（宋体），字形按墨迹外接框居中。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

const (
	// macOS 的应用图标网格：1024 的画布，824 的圆角方居中，圆角半径 185。
	iconSize   = 1024
	bodyInset  = 100.0
	bodyRadius = 185.0

	ss = 4 // 超采样倍数
)

// style 是一张图的样子：纯色底，加上字的大小。
type style struct {
	bg     color.NRGBA
	ink    float64 // 字墨迹的高度，相对图标边长
	shadow float64 // 字下方投影的 alpha（0 关闭）
}

func main() {
	out := flag.String("o", "resources/icon.png", "输出的 PNG 路径")
	size := flag.Int("size", iconSize, "输出边长（像素）")
	fontPath := flag.String("font", "/System/Library/Fonts/Supplemental/Songti.ttc", "字体文件")
	fontFace := flag.String("face", "Songti SC Bold", "TTC 里的字体全名")
	char := flag.String("char", "译", "中间的字")
	ink := flag.Float64("ink", 0.43, "字的墨迹高度，相对图标边长")
	bgHex := flag.String("bg", "#2b60d3", "底色（纯色，不渐变）")
	tshadow := flag.Float64("tshadow", 0x4e, "字的投影 alpha（0 关闭）")
	flag.Parse()

	st := style{
		bg:     parseHex(*bgHex),
		ink:    *ink,
		shadow: *tshadow,
	}
	img := resize(render(*fontPath, *fontFace, *char, st), *size)

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("%s %dx%d", *out, *size, *size)
}

// render 在 4 倍画布上完整画一遍。
func render(fontPath, faceName, char string, st style) *image.RGBA {
	n := iconSize * ss
	canvas := image.NewRGBA(image.Rect(0, 0, n, n))

	inset, radius := bodyInset*ss, bodyRadius*ss
	body := roundedRect(n, inset, float64(n)-inset, radius)

	// 投影：圆角方压成 alpha，模糊后偏下一点压在画布上。
	shadow := blur(body, 24*ss, 3)
	paint(canvas, color.NRGBA{0x0a, 0x1c, 0x50, 0x66}, shadow, 0, 10*ss)

	// 纯色底，一个颜色铺满，没有任何渐变或高光。
	draw.DrawMask(canvas, canvas.Bounds(), image.NewUniform(st.bg),
		image.Point{}, body, image.Point{}, draw.Src)

	drawChar(canvas, fontPath, faceName, char, st.ink, st.shadow)

	return canvas
}

// drawChar 把汉字按墨迹外接框居中画到画布上，字下方带一层柔和的投影。
func drawChar(dst *image.RGBA, fontPath, faceName, char string, ink, shadowAlpha float64) {
	data, err := os.ReadFile(fontPath)
	if err != nil {
		log.Fatalf("读字体 %s: %v", fontPath, err)
	}
	coll, err := opentype.ParseCollection(data)
	if err != nil {
		log.Fatalf("解析字体 %s: %v", fontPath, err)
	}
	f := pickFace(coll, faceName)

	// 墨迹高度跟字号成正比，量一个小号的就够算出目标字号。
	const probeSize = 400.0
	h := inkBoundsBox(renderChar(f, char, probeSize)).Dy()
	if h == 0 {
		log.Fatalf("字体里没有 %q", char)
	}
	size := ink * float64(dst.Bounds().Dy()) * probeSize / float64(h)

	mask := renderChar(f, char, size)
	b := inkBoundsBox(mask)
	if b.Empty() {
		log.Fatalf("字体里没有 %q", char)
	}

	n := dst.Bounds()
	// 按墨迹框居中；再往上提一点，汉字的视觉重心偏低。
	dx := n.Dx()/2 - (b.Min.X+b.Max.X)/2
	dy := n.Dy()/2 - (b.Min.Y+b.Max.Y)/2 - n.Dy()/100

	// 字的 alpha 落到整张画布上，投影和字本身都从它来。
	glyph := image.NewAlpha(n)
	draw.DrawMask(glyph, n.Add(image.Pt(dx, dy)), image.NewUniform(color.Alpha{0xff}),
		image.Point{}, mask, image.Point{}, draw.Over)

	if shadowAlpha > 0 {
		// 投影分两层：贴字的一层紧、外圈一层散，叠起来才不会像把字复制了一份。
		// 半径都按画布比例走，4 倍超采样画布和最终的 1024 看起来是同一层。
		h := float64(n.Dy())
		shade := func(a float64) color.NRGBA {
			return color.NRGBA{0x00, 0x0c, 0x38, uint8(math.Round(shadowAlpha * a))}
		}
		paint(dst, shade(0.55), blur(glyph, 0.042*h, 3), 0, int(math.Round(0.013*h)))
		paint(dst, shade(0.75), blur(glyph, 0.011*h, 2), 0, int(math.Round(0.006*h)))
	}

	draw.DrawMask(dst, n, image.NewUniform(color.NRGBA{0xff, 0xff, 0xff, 0xff}),
		image.Point{}, glyph, image.Point{}, draw.Over)
}

// renderChar 在给定字号下把字画成 alpha 蒙版，字大致落在画布中央，
// 位置不重要，后面按墨迹框搬。
func renderChar(f *opentype.Font, char string, size float64) *image.Alpha {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		log.Fatal(err)
	}
	defer face.Close()

	n := int(size * 1.8)
	mask := image.NewAlpha(image.Rect(0, 0, n, n))
	d := font.Drawer{
		Dst:  mask,
		Src:  image.NewUniform(color.Alpha{0xff}),
		Face: face,
		Dot:  fixed.P(n/2, n/2),
	}
	d.DrawString(char)
	return mask
}

// inkBoundsBox 返回蒙版里非零像素的外接框。
func inkBoundsBox(m *image.Alpha) image.Rectangle {
	b := m.Bounds()
	r := image.Rectangle{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := m.Pix[y*m.Stride : y*m.Stride+b.Dx()]
		for x, v := range row {
			if v == 0 {
				continue
			}
			px, py := b.Min.X+x, y
			if r.Empty() {
				r = image.Rect(px, py, px+1, py+1)
				continue
			}
			r.Min.X, r.Min.Y = min(r.Min.X, px), min(r.Min.Y, py)
			r.Max.X, r.Max.Y = max(r.Max.X, px+1), max(r.Max.Y, py+1)
		}
	}
	return r
}

func pickFace(coll *opentype.Collection, name string) *opentype.Font {
	var first string
	for i := 0; i < coll.NumFonts(); i++ {
		f, err := coll.Font(i)
		if err != nil {
			continue
		}
		full, _ := f.Name(nil, sfnt.NameIDFull)
		if full == name {
			return f
		}
		if first == "" {
			first = full
		}
	}
	log.Fatalf("字体里没有 %q（有 %q）", name, first)
	return nil
}

// roundedRect 返回圆角方的覆盖蒙版（0 或 255）。
func roundedRect(n int, inset, end, r float64) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		fy := float64(y) + 0.5
		for x := 0; x < n; x++ {
			fx := float64(x) + 0.5
			cx := math.Min(math.Max(fx, inset+r), end-r)
			cy := math.Min(math.Max(fy, inset+r), end-r)
			if math.Hypot(fx-cx, fy-cy) <= r {
				m.Pix[y*m.Stride+x] = 0xff
			}
		}
	}
	return m
}

// parseHex 读 #rrggbb。
func parseHex(s string) color.NRGBA {
	var r, g, b uint8
	if _, err := fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &r, &g, &b); err != nil {
		log.Fatalf("颜色 %q 不是 #rrggbb: %v", s, err)
	}
	return color.NRGBA{r, g, b, 0xff}
}

// paint 把颜色 c 以 mask 的 alpha 叠到 dst 上，可带偏移。
func paint(dst *image.RGBA, c color.NRGBA, mask *image.Alpha, dx, dy int) {
	layer := image.NewRGBA(dst.Bounds())
	draw.DrawMask(layer, dst.Bounds(), image.NewUniform(c), image.Point{}, mask, image.Point{}, draw.Over)
	draw.Draw(dst, dst.Bounds(), shifted(layer, dx, dy), image.Point{}, draw.Over)
}

// shifted 把图层平移 (dx, dy)，返回同一尺寸的新图。
func shifted(src *image.RGBA, dx, dy int) *image.RGBA {
	n := src.Bounds()
	out := image.NewRGBA(n)
	for y := 0; y < n.Dy(); y++ {
		sy := y - dy
		if sy < 0 || sy >= n.Dy() {
			continue
		}
		for x := 0; x < n.Dx(); x++ {
			sx := x - dx
			if sx < 0 || sx >= n.Dx() {
				continue
			}
			copy(out.Pix[out.PixOffset(x, y):out.PixOffset(x, y)+4], src.Pix[src.PixOffset(sx, sy):src.PixOffset(sx, sy)+4])
		}
	}
	return out
}

// blur 做 passes 遍盒式模糊，近似高斯。
func blur(src *image.Alpha, radius float64, passes int) *image.Alpha {
	r := max(1, int(math.Round(radius/float64(passes))))
	cur := src
	for i := 0; i < passes; i++ {
		cur = boxBlur(cur, r)
	}
	return cur
}

// boxBlur 是滑动窗口的盒式模糊，每个通道两趟，O(w*h)。
func boxBlur(src *image.Alpha, r int) *image.Alpha {
	w, h, stride := src.Bounds().Dx(), src.Bounds().Dy(), src.Stride
	tmp := image.NewAlpha(src.Bounds())
	out := image.NewAlpha(src.Bounds())

	span := 2*r + 1
	for y := 0; y < h; y++ {
		row := y * stride
		sum := 0
		for k := -r; k <= r; k++ {
			sum += int(src.Pix[row+min(max(k, 0), w-1)])
		}
		for x := 0; x < w; x++ {
			tmp.Pix[row+x] = uint8(sum / span)
			sum += int(src.Pix[row+min(max(x+1+r, 0), w-1)]) - int(src.Pix[row+min(max(x-r, 0), w-1)])
		}
	}
	for x := 0; x < w; x++ {
		sum := 0
		for k := -r; k <= r; k++ {
			sum += int(tmp.Pix[min(max(k, 0), h-1)*stride+x])
		}
		for y := 0; y < h; y++ {
			out.Pix[y*stride+x] = uint8(sum / span)
			sum += int(tmp.Pix[min(max(y+1+r, 0), h-1)*stride+x]) - int(tmp.Pix[min(max(y-r, 0), h-1)*stride+x])
		}
	}
	return out
}

// resize 用盒式滤波缩到 size×size；超采样后这一步就是抗锯齿。
func resize(src *image.RGBA, size int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		y0, y1 := y*sh/size, max((y+1)*sh/size, y*sh/size+1)
		for x := 0; x < size; x++ {
			x0, x1 := x*sw/size, max((x+1)*sw/size, x*sw/size+1)
			var r, g, b, a, n uint32
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					i := src.PixOffset(xx, yy)
					w := uint32(src.Pix[i+3])
					// 颜色按 alpha 预乘再平均，透明边缘不会发黑。
					r += uint32(src.Pix[i+0]) * w / 255
					g += uint32(src.Pix[i+1]) * w / 255
					b += uint32(src.Pix[i+2]) * w / 255
					a += w
					n++
				}
			}
			if a == 0 {
				continue
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o+0] = uint8(min(r*255/a, 255))
			dst.Pix[o+1] = uint8(min(g*255/a, 255))
			dst.Pix[o+2] = uint8(min(b*255/a, 255))
			dst.Pix[o+3] = uint8(a / n)
		}
	}
	return dst
}
