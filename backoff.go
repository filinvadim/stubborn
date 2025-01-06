package stubborn

import (
	"math"
	"math/rand"
	"time"
)

const eulersNumber = 2.71828

type backoff struct {
	attempt        float64
	Exponentiation float64
	//Jitter eases contention by randomizing backoff steps
	Jitter bool
	//Min and Max are the minimum and maximum values of the counter
	Min, Max time.Duration
}

func (b *backoff) Duration() time.Duration {
	d := b.forAttempt(b.attempt)
	b.attempt++
	return d
}

func (b *backoff) forAttempt(attempt float64) time.Duration {
	// Zero-values are nonsensical, so we use
	// them to apply defaults
	min := b.Min
	if min <= 0 {
		min = 100 * time.Millisecond
	}
	max := b.Max
	if max <= 0 {
		max = 10 * time.Second
	}
	if min >= max {
		// short-circuit
		return max
	}

	var exp = b.Exponentiation
	if b.Exponentiation == 0 {
		exp = eulersNumber
	}

	minf := float64(min)
	durf := minf * math.Pow(exp, attempt)
	if b.Jitter {
		durf = rand.Float64()*(durf-minf) + minf
	}

	if durf > math.MaxInt64 {
		return max
	}
	dur := time.Duration(durf)

	if dur < min {
		return min
	}
	if dur > max {
		return max
	}
	return dur
}

// Reset restarts the current attempt counter at zero.
func (b *backoff) Reset() {
	b.attempt = 0
}

// Attempt returns the current attempt counter value.
func (b *backoff) Attempt() float64 {
	return b.attempt
}
