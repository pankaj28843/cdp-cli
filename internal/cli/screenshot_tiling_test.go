package cli

import "testing"

func TestScreenshotTilesCoverage(t *testing.T) {
	tests := []struct {
		name          string
		contentHeight float64
		tileHeight    float64
		wantHeights   []float64
	}{
		{name: "short-page", contentHeight: 500, tileHeight: 800, wantHeights: []float64{500}},
		{name: "exact-fit", contentHeight: 1600, tileHeight: 800, wantHeights: []float64{800, 800}},
		{name: "one-extra-pixel", contentHeight: 1601, tileHeight: 800, wantHeights: []float64{800, 800, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tiles, err := screenshotTiles(1024, tt.contentHeight, tt.tileHeight, 10)
			if err != nil {
				t.Fatalf("screenshotTiles returned error: %v", err)
			}
			if len(tiles) != len(tt.wantHeights) {
				t.Fatalf("screenshotTiles returned %d tiles, want %d", len(tiles), len(tt.wantHeights))
			}
			var covered float64
			for i, tile := range tiles {
				if tile.Index != i || tile.Clip.Y != covered || tile.Clip.Width != 1024 || tile.Clip.Scale != 1 {
					t.Fatalf("tile %d = %+v, want contiguous 1024px-wide clip at y=%v", i, tile, covered)
				}
				if tile.Clip.Height != tt.wantHeights[i] {
					t.Fatalf("tile %d height = %v, want %v", i, tile.Clip.Height, tt.wantHeights[i])
				}
				covered += tile.Clip.Height
			}
			if covered != tt.contentHeight {
				t.Fatalf("covered height = %v, want %v", covered, tt.contentHeight)
			}
		})
	}
}

func TestScreenshotTilesRefusesTooManyTiles(t *testing.T) {
	_, err := screenshotTiles(1024, 2401, 800, 3)
	if err == nil {
		t.Fatal("screenshotTiles returned nil error, want tile-count refusal")
	}
}
