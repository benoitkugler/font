package sfnt

import (
	"fmt"
	"os"
	"testing"
)

func TestHtmx(t *testing.T) {
	for _, file := range []string{
		"testdata/Go-Regular.woff2",
		"testdata/Roboto-BoldItalic.ttf",
		"testdata/open-sans-v15-latin-regular.woff",
		"testdata/Raleway-v4020-Regular.otf",
		"testdata/Castoro-Regular.ttf",
		"testdata/Castoro-Italic.ttf",
		"testdata/FreeSerif.ttf",
		"testdata/AnjaliOldLipi-Regular.ttf",
	} {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		font, err := Parse(f)
		if err != nil {
			t.Fatal(err)
		}

		widths, err := font.HtmxTable()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("	widths:", len(widths))

		f.Close()
	}
}
