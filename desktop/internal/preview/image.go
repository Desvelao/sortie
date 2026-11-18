package preview

import (
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"sortie/internal/media"
)

const (
	maxPreviewDim = 1600        // longest edge kept in memory
	maxPixels     = 150_000_000 // refuse to decode absurdly large images
)

// ImageHandler previews raster images (decoded and downscaled) and SVG.
type ImageHandler struct{}

func (ImageHandler) CanHandle(mime, ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tif", ".tiff", ".webp", ".svg":
		return !strings.HasPrefix(mime, "text/") || strings.ToLower(ext) == ".svg"
	}
	return mime == "image/jpeg" || mime == "image/png" || mime == "image/gif" ||
		mime == "image/bmp" || mime == "image/tiff" || mime == "image/webp" || mime == "image/svg+xml"
}

func (ImageHandler) CreatePreview(path string) (Preview, error) {
	if strings.EqualFold(extOf(path), ".svg") {
		return Preview{Kind: KindSVG, Path: path}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return Preview{}, err
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return Preview{}, fmt.Errorf("cannot read image: %w", err)
	}
	p := Preview{Kind: KindImage, Fields: []Field{
		{"Format", strings.ToUpper(format)},
		{"Dimensions", fmt.Sprintf("%d × %d px", cfg.Width, cfg.Height)},
	}}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		p.Notes = append(p.Notes, "Image too large to preview")
		return p, nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return Preview{}, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return Preview{}, fmt.Errorf("cannot decode image: %w", err)
	}
	img = downscale(img, maxPreviewDim)

	if ex, err := media.ReadEXIF(path); err == nil {
		if !ex.Taken.IsZero() {
			p.Fields = append(p.Fields, Field{"Captured", ex.Taken.Format("2006-01-02 15:04:05")})
		}
		if ex.Camera != "" {
			p.Fields = append(p.Fields, Field{"Camera", ex.Camera})
		}
		img = orient(img, ex.Orientation)
	}
	p.Image = img
	return p, nil
}

func extOf(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i:]
	}
	return ""
}

// downscale shrinks img so its longest edge is at most max.
func downscale(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return img
	}
	nw, nh := max, max*h/w
	if h > w {
		nw, nh = max*w/h, max
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// orient applies an EXIF orientation (1–8) so photos display upright.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := w, h
	if o >= 5 {
		nw, nh = h, w
	}
	// Work on RGBA pixels directly: per-pixel Set/At on the image interfaces
	// costs an allocation and a dynamic call for every pixel.
	src := toRGBA(img)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < h; y++ {
		so := y * src.Stride
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			copy(dst.Pix[dy*dst.Stride+dx*4:dy*dst.Stride+dx*4+4], src.Pix[so+x*4:so+x*4+4])
		}
	}
	return dst
}

// toRGBA returns img as an *image.RGBA whose bounds start at (0,0), sharing
// the pixels when img already is one. draw.Draw has fast paths for the common
// decoded types (YCbCr, NRGBA, Gray, Paletted).
func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(r, r.Bounds(), img, b.Min, draw.Src)
	return r
}

// Thumbnail decodes a raster image directly to a small, upright thumbnail
// (longest edge max). It returns an error for non-raster or unreadable files.
func Thumbnail(path string, max int) (image.Image, error) {
	switch strings.ToLower(extOf(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tif", ".tiff", ".webp":
	default:
		return nil, fmt.Errorf("no thumbnail for this file type")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("image too large")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	img = downscale(img, max)
	if ex, err := media.ReadEXIF(path); err == nil {
		img = orient(img, ex.Orientation)
	}
	return img, nil
}
