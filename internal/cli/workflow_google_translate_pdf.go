package cli

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

const (
	googleTranslatePDFWidth  = 595.276
	googleTranslatePDFHeight = 841.890
)

type googleTranslatePDFImage struct {
	Width  int
	Height int
	Data   []byte
}

// assembleGooglePNGPDF writes a small, standards-compliant image PDF using
// only the Go standard library. Keeping assembly inside cdp-cli makes the
// scanned-PDF path portable and avoids silently depending on Python,
// ImageMagick, or a second browser tool.
func assembleGooglePNGPDF(paths []string, outputPath string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no translated PNG pages")
	}
	images := make([]googleTranslatePDFImage, 0, len(paths))
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open translated PNG %s: %w", path, err)
		}
		decoded, err := png.Decode(file)
		_ = file.Close()
		if err != nil {
			return fmt.Errorf("decode translated PNG %s: %w", path, err)
		}
		encoded, err := googleTranslateRGBStream(decoded)
		if err != nil {
			return fmt.Errorf("encode translated PNG %s for PDF: %w", path, err)
		}
		bounds := decoded.Bounds()
		images = append(images, googleTranslatePDFImage{Width: bounds.Dx(), Height: bounds.Dy(), Data: encoded})
	}

	objectCount := 2 + len(images)*3
	offsets := make([]int, objectCount+1)
	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	writeObject := func(number int, body []byte) {
		offsets[number] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n", number)
		document.Write(body)
		if len(body) == 0 || body[len(body)-1] != '\n' {
			document.WriteByte('\n')
		}
		document.WriteString("endobj\n")
	}

	var catalog bytes.Buffer
	fmt.Fprintf(&catalog, "<< /Type /Catalog /Pages 2 0 R >>\n")
	writeObject(1, catalog.Bytes())
	var pages bytes.Buffer
	fmt.Fprintf(&pages, "<< /Type /Pages /Kids [")
	for index := range images {
		fmt.Fprintf(&pages, "%d 0 R ", 3+index*3)
	}
	fmt.Fprintf(&pages, "] /Count %d >>\n", len(images))
	writeObject(2, pages.Bytes())

	for index, pageImage := range images {
		pageNumber := 3 + index*3
		contentNumber := pageNumber + 1
		imageNumber := pageNumber + 2
		scale := googleTranslateFitScale(pageImage.Width, pageImage.Height)
		width := float64(pageImage.Width) * scale
		height := float64(pageImage.Height) * scale
		x := (googleTranslatePDFWidth - width) / 2
		y := (googleTranslatePDFHeight - height) / 2
		content := []byte(fmt.Sprintf("q %.5f 0 0 %.5f %.5f %.5f cm /Im0 Do Q\n", width, height, x, y))
		var page bytes.Buffer
		fmt.Fprintf(&page, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.3f %.3f] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>\n", googleTranslatePDFWidth, googleTranslatePDFHeight, imageNumber, contentNumber)
		writeObject(pageNumber, page.Bytes())
		var contentObject bytes.Buffer
		fmt.Fprintf(&contentObject, "<< /Length %d >>\nstream\n", len(content))
		contentObject.Write(content)
		contentObject.WriteString("endstream\n")
		writeObject(contentNumber, contentObject.Bytes())
		var imageObject bytes.Buffer
		fmt.Fprintf(&imageObject, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", pageImage.Width, pageImage.Height, len(pageImage.Data))
		imageObject.Write(pageImage.Data)
		imageObject.WriteString("\nendstream\n")
		writeObject(imageNumber, imageObject.Bytes())
	}

	xrefOffset := document.Len()
	document.WriteString("xref\n")
	fmt.Fprintf(&document, "0 %d\n", objectCount+1)
	document.WriteString("0000000000 65535 f \n")
	for number := 1; number <= objectCount; number++ {
		fmt.Fprintf(&document, "%010d 00000 n \n", offsets[number])
	}
	document.WriteString("trailer\n")
	fmt.Fprintf(&document, "<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", objectCount+1, xrefOffset)
	if err := artifacts.WriteOwnerOnlyFileAtomic(outputPath, document.Bytes()); err != nil {
		return err
	}
	return nil
}

func googleTranslateFitScale(width, height int) float64 {
	if width <= 0 || height <= 0 {
		return 1
	}
	widthScale := googleTranslatePDFWidth / float64(width)
	heightScale := googleTranslatePDFHeight / float64(height)
	if widthScale < heightScale {
		return widthScale
	}
	return heightScale
}

func googleTranslateRGBStream(input image.Image) ([]byte, error) {
	var raw bytes.Buffer
	bounds := input.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := input.At(x, y).RGBA()
			r, g, b = googleTranslateCompositeOnWhite(r, g, b, a)
			raw.WriteByte(byte(r >> 8))
			raw.WriteByte(byte(g >> 8))
			raw.WriteByte(byte(b >> 8))
		}
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(raw.Bytes()); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func googleTranslateCompositeOnWhite(r, g, b, a uint32) (uint32, uint32, uint32) {
	if a < 0xffff {
		background := uint32(0xffff) - a
		r = uint32((uint64(r)*uint64(a) + uint64(0xffff)*uint64(background)) / uint64(0xffff))
		g = uint32((uint64(g)*uint64(a) + uint64(0xffff)*uint64(background)) / uint64(0xffff))
		b = uint32((uint64(b)*uint64(a) + uint64(0xffff)*uint64(background)) / uint64(0xffff))
	}
	return r, g, b
}
