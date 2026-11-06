// Command icongen 画出应用图标：一块蓝色圆角方，中间一个白色的宋体「译」。
//
//	go run ./tools/icongen                 # 写 resources/icon.png
//	go run ./tools/icongen -o /tmp/i.png   # 换个地方看效果
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

// style 是一张图的样子：渐变的两个端点，加上高光与字的比例。
type style struct {
	top, bottom color.NRGBA
	sheen       float64 // 顶部高光的 alpha
	ink         float64 // 字墨迹的高度，相对图标边长
}

func main() {
	out := flag.String("o", "resources/icon.png", "输出的 PNG 路径")
	size := flag.Int("size", iconSize, "输出边长（像素）")
	fontPath := flag.String("font", "/System/Library/Fonts/Supplemental/Songti.ttc", "字体文件")
	fontFace := flag.String("face", "Songti SC Bold", "TTC 里的字体全名")
	char := flag.String("char", "译", "中间的字")
	ink := flag.Float64("ink", 0.43, "字的墨迹高度，相对图标边长")
	sheen := flag.Float64("sheen", 0x38, "顶部高光的 alpha（0-255）")
	topHex := flag.String("top", "#4c8dff", "渐变起点颜色")
	bottomHex := flag.String("bottom", "#0b33a8", "渐变终点颜色")
	flag.Parse()

	st := style{
		top:    parseHex(*topHex),
		bottom: parseHex(*bottomHex),
		sheen:  *sheen,
		ink:    *ink,
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

	// 底色渐变，自上而下略向右倾。
	draw.DrawMask(canvas, canvas.Bounds(),
		gradient(n, st.top, st.bottom, 0.18, 1, inset, float64(n)-inset),
		image.Point{}, body, image.Point{}, draw.Src)

	// 顶部一层白，像光从左上方打过来；底部压一点暗，别太扁平。
	draw.DrawMask(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{0xff, 0xff, 0xff, 0xff}),
		image.Point{}, intersect(verticalFade(n, inset, inset+0.5*(float64(n)-2*inset), st.sheen), body),
		image.Point{}, draw.Over)
	draw.DrawMask(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{0x00, 0x0a, 0x33, 0xff}),
		image.Point{}, intersect(verticalFade(n, float64(n)-inset, float64(n)-inset-0.35*(float64(n)-2*inset), 0x30), body),
		image.Point{}, draw.Over)

	// 顶部一圈细高光，把边勾出来，缩到 Dock 尺寸时轮廓更清楚。
	ring := subtract(body, roundedRect(n, inset+2.5*ss, float64(n)-inset-2.5*ss, radius-2.5*ss))
	draw.DrawMask(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{0xff, 0xff, 0xff, 0xff}),
		image.Point{}, intersect(verticalFade(n, inset, inset+0.42*(float64(n)-2*inset), 0x2e), ring),
		image.Point{}, draw.Over)

	drawChar(canvas, fontPath, faceName, char, st.ink)

	return canvas
}

// drawChar 把汉字按墨迹外接框居中画到画布上。
func drawChar(dst *image.RGBA, fontPath, faceName, char string, ink float64) {
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

	layer := image.NewRGBA(n)
	draw.DrawMask(layer, n, image.NewUniform(color.NRGBA{0xff, 0xff, 0xff, 0xff}), image.Point{}, mask, image.Point{}, draw.Over)
	draw.Draw(dst, n, shifted(layer, dx, dy), image.Point{}, draw.Over)
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

// gradient 生成沿 (dx, dy) 从 a 到 b 的线性渐变，范围是 [lo, hi] 的方块。
func gradient(n int, a, b color.NRGBA, dx, dy, lo, hi float64) *image.RGBA {
	l := math.Hypot(dx, dy)
	dx, dy = dx/l, dy/l
	proj := func(x, y float64) float64 { return (x-lo)*dx + (y-lo)*dy }
	minP, maxP := math.Inf(1), math.Inf(-1)
	for _, c := range [][2]float64{{lo, lo}, {hi, lo}, {lo, hi}, {hi, hi}} {
		p := proj(c[0], c[1])
		minP, maxP = math.Min(minP, p), math.Max(maxP, p)
	}

	g := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			t := (proj(float64(x)+0.5, float64(y)+0.5) - minP) / (maxP - minP)
			t = math.Min(math.Max(t, 0), 1)
			t = t * t * (3 - 2*t) // smoothstep，两端过渡更柔
			c := lerp(a, b, t)
			i := g.PixOffset(x, y)
			g.Pix[i+0], g.Pix[i+1], g.Pix[i+2], g.Pix[i+3] = c.R, c.G, c.B, 0xff
		}
	}
	return g
}

// verticalFade 从 y0 的 alpha 渐隐到 y1 的 0；反向则从 0 渐显。
func verticalFade(n int, y0, y1, alpha float64) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		f := float64(y) + 0.5
		t := (f - y0) / (y1 - y0)
		t = math.Min(math.Max(t, 0), 1)
		a := alpha * (1 - t*t)
		for x := 0; x < n; x++ {
			m.Pix[y*m.Stride+x] = uint8(a)
		}
	}
	return m
}

// intersect 取两张蒙版里较小的 alpha。
func intersect(a, b *image.Alpha) *image.Alpha {
	n := a.Bounds()
	out := image.NewAlpha(n)
	for y := 0; y < n.Dy(); y++ {
		for x := 0; x < n.Dx(); x++ {
			out.Pix[y*out.Stride+x] = min(a.Pix[y*a.Stride+x], b.Pix[y*b.Stride+x])
		}
	}
	return out
}

// subtract 返回 a 里挖掉 b 的部分。
func subtract(a, b *image.Alpha) *image.Alpha {
	n := a.Bounds()
	out := image.NewAlpha(n)
	for y := 0; y < n.Dy(); y++ {
		for x := 0; x < n.Dx(); x++ {
			out.Pix[y*out.Stride+x] = uint8(int(a.Pix[y*a.Stride+x]) * (255 - int(b.Pix[y*b.Stride+x])) / 255)
		}
	}
	return out
}

// parseHex 读 #rrggbb。
func parseHex(s string) color.NRGBA {
	var r, g, b uint8
	if _, err := fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &r, &g, &b); err != nil {
		log.Fatalf("颜色 %q 不是 #rrggbb: %v", s, err)
	}
	return color.NRGBA{r, g, b, 0xff}
}

func lerp(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		0xff,
	}
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
