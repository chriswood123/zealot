package pipeline

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var normalRe = regexp.MustCompile(`^normal\(\s*([^,]+)\s*,\s*([^)]+)\s*\)$`)

// SampleDistribution parses and samples a distribution spec. Supported:
//   - normal(mean,stddev) — e.g. "normal(50ms,5ms)"
//   - uniform(min,max)    — e.g. "uniform(10ms,100ms)"
//   - fixed duration      — e.g. "50ms"
func SampleDistribution(spec string) (time.Duration, error) {
	spec = strings.TrimSpace(spec)
	if m := normalRe.FindStringSubmatch(spec); m != nil {
		mean, err := time.ParseDuration(strings.TrimSpace(m[1]))
		if err != nil {
			return 0, fmt.Errorf("distribution %q: bad mean: %w", spec, err)
		}
		std, err := time.ParseDuration(strings.TrimSpace(m[2]))
		if err != nil {
			return 0, fmt.Errorf("distribution %q: bad stddev: %w", spec, err)
		}
		d := time.Duration(float64(mean) + rand.NormFloat64()*float64(std))
		if d < 0 {
			d = 0
		}
		return d, nil
	}
	if strings.HasPrefix(spec, "uniform(") && strings.HasSuffix(spec, ")") {
		parts := strings.SplitN(spec[len("uniform("):len(spec)-1], ",", 2)
		if len(parts) != 2 {
			return 0, fmt.Errorf("distribution %q: uniform requires min,max", spec)
		}
		lo, err := time.ParseDuration(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, fmt.Errorf("distribution %q: bad min: %w", spec, err)
		}
		hi, err := time.ParseDuration(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, fmt.Errorf("distribution %q: bad max: %w", spec, err)
		}
		if hi < lo {
			return 0, fmt.Errorf("distribution %q: max < min", spec)
		}
		return lo + time.Duration(rand.Float64()*float64(hi-lo)), nil
	}
	return time.ParseDuration(spec)
}

var sizeRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(b|kb|mb|gb)?$`)

// ParseSize parses sizes like "512", "2kb", "1.5mb" into bytes.
func ParseSize(s string) (int, error) {
	m := sizeRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	mult := 1.0
	switch m[2] {
	case "kb":
		mult = 1 << 10
	case "mb":
		mult = 1 << 20
	case "gb":
		mult = 1 << 30
	}
	return int(n * mult), nil
}
