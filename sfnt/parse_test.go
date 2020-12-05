package sfnt

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestParseCrashers(t *testing.T) {
	font, err := Parse(bytes.NewReader([]byte{}))
	if font != nil || err == nil {
		t.Fail()
	}

	for range [50]int{} {
		input := make([]byte, 20000)
		rand.Read(input)
		font, err = Parse(bytes.NewReader(input))
		if font != nil || err == nil {
			t.Error("expected error on random input")
		}
	}
}
