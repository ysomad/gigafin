package useragent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ysomad/gigafin/internal/useragent"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give string
		want useragent.Agent
	}{
		{
			name: "safari on iphone",
			give: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) " +
				"Version/18.6 Mobile/15E148 Safari/604.1",
			want: useragent.Agent{Browser: "Safari", OS: "iPhone"},
		},
		{
			name: "chrome on iphone",
			give: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) " +
				"CriOS/140.0.0.0 Mobile/15E148 Safari/604.1",
			want: useragent.Agent{Browser: "Chrome", OS: "iPhone"},
		},
		{
			name: "firefox on macos",
			give: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:160.0) Gecko/20100101 Firefox/160.0",
			want: useragent.Agent{Browser: "Firefox", OS: "macOS"},
		},
		{
			name: "chrome on macos",
			give: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) " +
				"Chrome/157.0.0.0 Safari/537.36",
			want: useragent.Agent{Browser: "Chrome", OS: "macOS"},
		},
		{
			name: "edge on windows",
			give: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) " +
				"Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0",
			want: useragent.Agent{Browser: "Edge", OS: "Windows"},
		},
		{
			name: "yandex on android",
			give: "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) " +
				"Chrome/138.0.0.0 YaBrowser/25.8.1 Mobile Safari/537.36",
			want: useragent.Agent{Browser: "Yandex Browser", OS: "Android"},
		},
		{
			name: "unknown",
			give: "curl/8.7.1",
			want: useragent.Agent{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, useragent.Parse(tt.give))
		})
	}
}
