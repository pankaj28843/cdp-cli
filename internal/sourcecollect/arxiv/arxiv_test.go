package arxiv

import "testing"

func TestParseCanonicalizesArxivPaperURLs(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{{"https://arxiv.org/abs/2604.12374", "2604.12374"}, {"https://www.arxiv.org/html/2604.12374v2", "2604.12374v2"}, {"https://arxiv.org/pdf/2604.12374.pdf", "2604.12374"}, {"https://arxiv.org/abs/hep-th/9901001v3", "hep-th/9901001v3"}} {
		got, err := Parse(tt.raw)
		if err != nil || got.Identifier != tt.want {
			t.Fatalf("Parse(%q) = %+v, %v", tt.raw, got, err)
		}
	}
	for _, raw := range []string{"http://arxiv.org/abs/2604.12374", "https://arxiv.org/abs/0000.0000", "https://arxiv.org/abs/2613.12345", "https://arxiv.org/abs/1412.12345", "https://arxiv.org/search/?query=x", "https://arxiv.org.example/abs/2604.12374"} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("Parse(%q) succeeded", raw)
		}
	}
}

func TestValidateFinalURLPinsResolvedVersion(t *testing.T) {
	request, _ := Parse("https://arxiv.org/abs/2604.12374")
	final, err := ValidateFinalURL(request, "https://arxiv.org/html/2604.12374v2")
	if err != nil || final.Identifier != "2604.12374v2" {
		t.Fatalf("ValidateFinalURL() = %+v, %v", final, err)
	}
	pinned, _ := Parse("https://arxiv.org/abs/2604.12374v2")
	if _, err := ValidateFinalURL(pinned, "https://arxiv.org/html/2604.12374v3"); err == nil {
		t.Fatal("version drift accepted")
	}
}
