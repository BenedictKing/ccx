package config

import (
	"reflect"
	"testing"
)

func TestNormalizeContextWindowTiers(t *testing.T) {
	cases := []struct {
		name  string
		in    []int
		want  []int
		isNil bool
	}{
		{name: "空", in: nil, isNil: true},
		{name: "全非正值", in: []int{0, -5}, isNil: true},
		{name: "乱序去重", in: []int{1050000, 272000, 372000, 272000}, want: []int{272000, 372000, 1050000}},
		{name: "单档", in: []int{128000}, want: []int{128000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeContextWindowTiers(tc.in)
			if tc.isNil {
				if got != nil {
					t.Fatalf("want nil, got %v", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMaxKnownWindowTokens(t *testing.T) {
	cases := []struct {
		name string
		cap  UpstreamModelCapability
		want int
	}{
		{
			name: "无分段取声明窗口",
			cap:  UpstreamModelCapability{ContextWindowTokens: 272000},
			want: 272000,
		},
		{
			name: "分段末位高于声明",
			cap:  UpstreamModelCapability{ContextWindowTokens: 1050000, ContextWindowTiers: []int{272000, 372000, 1050000}},
			want: 1050000,
		},
		{
			name: "声明高于分段末位（数据异常时取更大者）",
			cap:  UpstreamModelCapability{ContextWindowTokens: 1050000, ContextWindowTiers: []int{272000}},
			want: 1050000,
		},
		{
			name: "零值",
			cap:  UpstreamModelCapability{},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cap.MaxKnownWindowTokens(); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
