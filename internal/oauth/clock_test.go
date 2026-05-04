package oauth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRealClock_DelegatesToTimePackage(t *testing.T) {
	clock := realClock{}
	before := time.Now()
	assert.WithinDuration(t, time.Now(), clock.Now(), 100*time.Millisecond)
	assert.GreaterOrEqual(t, clock.Since(before), time.Duration(0))

	fired := make(chan struct{}, 1)
	timer := clock.AfterFunc(5*time.Millisecond, func() {
		fired <- struct{}{}
	})
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("real clock timer did not fire")
	}
	assert.False(t, timer.Stop())
}
