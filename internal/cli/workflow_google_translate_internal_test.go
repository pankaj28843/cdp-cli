package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeGoogleTranslateLanguage(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		source bool
		want   string
	}{
		{name: "source auto", value: "detect", source: true, want: "auto"},
		{name: "Danish", value: "Danish", source: true, want: "da"},
		{name: "English", value: "English", source: false, want: "en"},
		{name: "regional Chinese", value: "zh-cn", source: false, want: "zh-CN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeGoogleTranslateLanguage(test.value, test.source)
			if err != nil || got != test.want {
				t.Fatalf("normalize(%q, source=%t) = %q, %v; want %q", test.value, test.source, got, err, test.want)
			}
		})
	}
	if _, err := normalizeGoogleTranslateLanguage("auto", false); err == nil {
		t.Fatal("target auto must be rejected")
	}
}

func TestGoogleTranslateTextChunksPreserveInputAndBound(t *testing.T) {
	input := strings.Repeat("å", 17) + " first paragraph\n\n" + strings.Repeat("B", 31) + " final"
	chunks := googleTranslateTextChunks(input, 20)
	if len(chunks) < 3 {
		t.Fatalf("chunks = %#v, want multiple chunks", chunks)
	}
	if strings.Join(chunks, "") != input {
		t.Fatalf("chunk concatenation changed input: %q != %q", strings.Join(chunks, ""), input)
	}
	for index, chunk := range chunks {
		if len([]rune(chunk)) > 20 {
			t.Fatalf("chunk %d has %d runes", index, len([]rune(chunk)))
		}
	}
}

func TestGoogleTranslateTextChunksPreferParagraphBoundaries(t *testing.T) {
	input := strings.Repeat("first ", 30) + "\n\n" + strings.Repeat("second ", 30)
	chunks := googleTranslateTextChunks(input, 200)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %#v, want at least two chunks", chunks)
	}
	if !strings.HasSuffix(chunks[0], "\n\n") {
		t.Fatalf("first chunk should end at paragraph boundary: %q", chunks[0])
	}
	if strings.Join(chunks, "") != input {
		t.Fatalf("paragraph-aware chunking changed input")
	}
}

func TestValidateGoogleTranslateOutput(t *testing.T) {
	textInput := googleTranslateInput{Path: "notes.txt"}
	if err := validateGoogleTranslateOutput("result.txt", textInput, "text"); err != nil {
		t.Fatal(err)
	}
	if err := validateGoogleTranslateOutput("result.pdf", textInput, "text"); err == nil {
		t.Fatal("text output should reject PDF extension")
	}
	documentInput := googleTranslateInput{Path: "report.docx"}
	if err := validateGoogleTranslateOutput("translated.docx", documentInput, "document"); err != nil {
		t.Fatal(err)
	}
	if err := validateGoogleTranslateOutput("translated.pdf", documentInput, "document"); err == nil {
		t.Fatal("document output should match the input extension")
	}
	imageInput := googleTranslateInput{Path: "page.png"}
	if err := validateGoogleTranslateOutput("translated.pdf", imageInput, "image"); err != nil {
		t.Fatal(err)
	}
	scanInput := googleTranslateInput{Path: "scan.pdf"}
	if err := validateGoogleTranslateOutput("translated.png", scanInput, "image"); err == nil {
		t.Fatal("scanned PDF output should be a PDF")
	}
}

func TestAssembleGooglePNGPDF(t *testing.T) {
	tempDir := t.TempDir()
	paths := []string{}
	for index, fill := range []color.RGBA{{R: 220, A: 255}, {B: 220, A: 255}} {
		path := filepath.Join(tempDir, "page-"+string(rune('1'+index))+".png")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		imageData := image.NewRGBA(image.Rect(0, 0, 12, 18))
		for y := 0; y < 18; y++ {
			for x := 0; x < 12; x++ {
				imageData.SetRGBA(x, y, fill)
			}
		}
		if err := png.Encode(file, imageData); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	output := filepath.Join(tempDir, "translated.pdf")
	if err := assembleGooglePNGPDF(paths, output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) || !bytes.Contains(data, []byte("/Count 2")) || !bytes.Contains(data, []byte("%%EOF")) {
		t.Fatalf("assembled PDF has unexpected header/trailer: %q", data[:minGoogleTranslateTestInt(len(data), 80)])
	}
}

func TestGoogleDocumentFileSelector(t *testing.T) {
	if got := googleDocumentFileSelector("report.pdf"); got != `input[type=file][accept*=".pdf"]` {
		t.Fatalf("PDF selector = %q", got)
	}
	if got := googleDocumentFileSelector("report.docx"); got != `input[type=file][accept*=".docx"]` {
		t.Fatalf("DOCX selector = %q", got)
	}
}

func minGoogleTranslateTestInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
