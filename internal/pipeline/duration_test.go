package pipeline

import (
	"testing"
	"time"
)

func TestSampleDistributionFixed(t *testing.T) {
	d, err := SampleDistribution("50ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != 50*time.Millisecond {
		t.Fatalf("got %v, want 50ms", d)
	}
}

func TestSampleDistributionNormal(t *testing.T) {
	const n = 5000
	var sum time.Duration
	for i := 0; i < n; i++ {
		d, err := SampleDistribution("normal(50ms,5ms)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d < 0 {
			t.Fatalf("negative duration %v", d)
		}
		sum += d
	}
	mean := sum / n
	if mean < 45*time.Millisecond || mean > 55*time.Millisecond {
		t.Fatalf("mean %v outside expected band around 50ms", mean)
	}
}

func TestSampleDistributionUniform(t *testing.T) {
	for i := 0; i < 1000; i++ {
		d, err := SampleDistribution("uniform(10ms,100ms)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d < 10*time.Millisecond || d > 100*time.Millisecond {
			t.Fatalf("duration %v outside [10ms,100ms]", d)
		}
	}
}

func TestSampleDistributionInvalid(t *testing.T) {
	for _, spec := range []string{"normal(50ms)", "uniform(10ms)", "bogus"} {
		if _, err := SampleDistribution(spec); err == nil {
			t.Fatalf("expected error for %q", spec)
		}
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int{
		"512":   512,
		"2kb":   2048,
		"1.5kb": 1536,
		"1mb":   1 << 20,
		"1KB":   1024,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil {
			t.Fatalf("ParseSize(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := ParseSize("ten"); err == nil {
		t.Fatal("expected error for invalid size")
	}
}

func TestRenderTemplate(t *testing.T) {
	fixed := func(int) int { return 42 }
	got := renderTemplate("item:{rand:1000}:{rand:10}", fixed)
	if got != "item:42:42" {
		t.Fatalf("got %q", got)
	}
	// No template left untouched.
	if s := renderTemplate("plain", fixed); s != "plain" {
		t.Fatalf("got %q", s)
	}
}
