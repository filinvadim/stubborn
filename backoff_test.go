package stubborn

import (
	"reflect"
	"testing"
	"time"
)

func TestBackoff_Duration(t *testing.T) {
	b := &backoff{
		Min:            100 * time.Millisecond,
		Max:            10 * time.Second,
		Exponentiation: 2,
	}

	equals(t, b.Duration(), 100*time.Millisecond)
	equals(t, b.Duration(), 200*time.Millisecond)
	equals(t, b.Duration(), 400*time.Millisecond)
	equals(t, b.Duration(), 800*time.Millisecond)
	b.Reset()
	equals(t, b.Duration(), 100*time.Millisecond)
}

func TestBackoff_Duration2(t *testing.T) {
	b := &backoff{
		Min:            100 * time.Millisecond,
		Max:            10 * time.Second,
		Exponentiation: 1.5,
	}

	equals(t, b.Duration(), 100*time.Millisecond)
	equals(t, b.Duration(), 150*time.Millisecond)
	equals(t, b.Duration(), 225*time.Millisecond)
	b.Reset()
	equals(t, b.Duration(), 100*time.Millisecond)
}

func TestBackoff_Duration3(t *testing.T) {
	b := &backoff{
		Min:            100 * time.Nanosecond,
		Max:            10 * time.Second,
		Exponentiation: 1.75,
	}

	equals(t, b.Duration(), 100*time.Nanosecond)
	equals(t, b.Duration(), 175*time.Nanosecond)
	equals(t, b.Duration(), 306*time.Nanosecond)
	b.Reset()
	equals(t, b.Duration(), 100*time.Nanosecond)
}

func TestBackoff_Max(t *testing.T) {
	b := &backoff{
		Min:            500 * time.Second,
		Max:            100 * time.Second,
		Exponentiation: 1,
	}

	equals(t, b.Duration(), b.Max)
}

func TestBackoff_Attempt(t *testing.T) {
	b := &backoff{
		Min:            100 * time.Millisecond,
		Max:            10 * time.Second,
		Exponentiation: 2,
	}
	equals(t, b.Attempt(), float64(0))
	equals(t, b.Duration(), 100*time.Millisecond)
	equals(t, b.Attempt(), float64(1))
	equals(t, b.Duration(), 200*time.Millisecond)
	equals(t, b.Attempt(), float64(2))
	equals(t, b.Duration(), 400*time.Millisecond)
	equals(t, b.Attempt(), float64(3))
	b.Reset()
	equals(t, b.Attempt(), float64(0))
	equals(t, b.Duration(), 100*time.Millisecond)
	equals(t, b.Attempt(), float64(1))
}

func TestBackoff_Jitter(t *testing.T) {
	b := &backoff{
		Min:            100 * time.Millisecond,
		Max:            10 * time.Second,
		Exponentiation: 2,
		Jitter:         true,
	}

	equals(t, b.Duration(), 100*time.Millisecond)
	between(t, b.Duration(), 100*time.Millisecond, 200*time.Millisecond)
	between(t, b.Duration(), 100*time.Millisecond, 400*time.Millisecond)
	b.Reset()
	equals(t, b.Duration(), 100*time.Millisecond)
}

func between(t *testing.T, actual, low, high time.Duration) {
	if actual < low {
		t.Fatalf("Got %s, Expecting >= %s", actual, low)
	}
	if actual > high {
		t.Fatalf("Got %s, Expecting <= %s", actual, high)
	}
}

func equals(t *testing.T, v1, v2 interface{}) {
	if !reflect.DeepEqual(v1, v2) {
		t.Logf("Got %v, Expecting %v", v1, v2)
		t.Fail()
	}
}
