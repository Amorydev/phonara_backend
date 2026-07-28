package service

import (
	"testing"
	"time"
)

func TestAssessmentTaskTimeoutOutlivesEngineCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		engineTimeout time.Duration
		want          time.Duration
	}{
		{
			name:          "production cold inference budget",
			engineTimeout: 2 * time.Minute,
			want:          150 * time.Second,
		},
		{
			name:          "custom deployment timeout",
			engineTimeout: 45 * time.Second,
			want:          75 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := assessmentTaskTimeout(tt.engineTimeout)
			if got != tt.want {
				t.Fatalf("assessmentTaskTimeout(%s) = %s, want %s",
					tt.engineTimeout, got, tt.want)
			}
			if got <= tt.engineTimeout {
				t.Fatalf("task timeout %s must outlive engine timeout %s",
					got, tt.engineTimeout)
			}
		})
	}
}
