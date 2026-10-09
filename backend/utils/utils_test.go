package utils

import "testing"

func TestTwoPointRound(t *testing.T) {
	type args struct {
		f float64
	}
	tests := []struct {
		name string
		args args
		want float64
	}{{"1.24", args{1.24}, 1.2},
		{"1.25", args{1.25}, 1.3},
		{"-1.56", args{-1.56}, -1.6},
		{"-1.55", args{-1.55}, -1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TwoPointRound(tt.args.f); got != tt.want {
				t.Errorf("TwoPointRound() = %v, want %v", got, tt.want)
			}
		})
	}

}
func TestTwoPointRound2(t *testing.T) {
	type args struct {
		f float64
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{"1.24", args{1.24}, 1},
		{"2.45", args{2.45}, 3},
		{"-2.56", args{-2.56}, -3},
		{"-2.55", args{-2.55}, -2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TwoPointRound2(tt.args.f); got != tt.want {
				t.Errorf("TwoPointRound() = %v, want %v", got, tt.want)
			}
		})
	}
}
