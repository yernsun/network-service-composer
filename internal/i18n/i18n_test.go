package i18n

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSupportedCodes(t *testing.T) {
	want := []string{"en", "ja", "de", "zh-CN", "zh-TW", "fr", "ru", "es"}
	if got := SupportedCodes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SupportedCodes() = %v, want %v", got, want)
	}
}

func TestResolveLanguagePreference(t *testing.T) {
	tests := []struct {
		name     string
		cookie   string
		accept   string
		fallback string
		want     Locale
	}{
		{name: "Spanish region", accept: "es-MX", fallback: "en", want: Spanish},
		{name: "Russian region", accept: "ru-RU", fallback: "en", want: Russian},
		{name: "cookie wins", cookie: "de", accept: "fr", fallback: "en", want: German},
		{name: "quality weights", accept: "de;q=0.4, ja;q=0.9", fallback: "en", want: Japanese},
		{name: "zh alias", accept: "zh", fallback: "en", want: SimplifiedChinese},
		{name: "zh Hans alias", accept: "zh-Hans", fallback: "en", want: SimplifiedChinese},
		{name: "zh Singapore alias", accept: "zh-SG", fallback: "en", want: SimplifiedChinese},
		{name: "zh Hant alias", accept: "zh-Hant", fallback: "en", want: TraditionalChinese},
		{name: "zh Taiwan alias", accept: "zh-TW", fallback: "en", want: TraditionalChinese},
		{name: "zh Hong Kong alias", accept: "zh-HK", fallback: "en", want: TraditionalChinese},
		{name: "zh Macao alias", accept: "zh-MO", fallback: "en", want: TraditionalChinese},
		{name: "unsupported", accept: "ko-KR", fallback: "fr", want: French},
		{name: "malicious cookie", cookie: "en\r\nSet-Cookie: bad=1", accept: "de", fallback: "en", want: German},
		{name: "malformed accept", accept: "not a language;;;", fallback: "en", want: English},
		{name: "invalid fallback", fallback: "invalid", want: English},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Resolve(test.cookie, test.accept, test.fallback); got != test.want {
				t.Fatalf("Resolve(%q, %q, %q) = %q, want %q", test.cookie, test.accept, test.fallback, got, test.want)
			}
		})
	}
}

func TestCatalogsAndInterpolation(t *testing.T) {
	var baseline map[string]string
	for _, option := range Supported() {
		data, ok := CatalogJSON(option.Code)
		if !ok {
			t.Fatalf("catalog %q is missing", option.Code)
		}
		var values map[string]string
		if err := json.Unmarshal(data, &values); err != nil {
			t.Fatalf("catalog %q is invalid JSON: %v", option.Code, err)
		}
		if len(values) < 381 {
			t.Fatalf("catalog %q contains only %d keys", option.Code, len(values))
		}
		if baseline == nil {
			baseline = values
		} else {
			if len(values) != len(baseline) {
				t.Fatalf("catalog %q contains %d keys, baseline contains %d", option.Code, len(values), len(baseline))
			}
			for key := range baseline {
				if _, exists := values[key]; !exists {
					t.Fatalf("catalog %q is missing key %q", option.Code, key)
				}
			}
		}
		if got := Text(option.Code, "problem.validation.dns_ref_unconfigured", map[string]any{"reference": "prod"}); got == "" || got == "problem.validation.dns_ref_unconfigured" {
			t.Fatalf("catalog %q did not render interpolation: %q", option.Code, got)
		}
	}
	if got := Text(German, "missing.key", nil); got != "missing.key" {
		t.Fatalf("missing key fallback = %q", got)
	}
	if got := Text(Locale("ko"), "nav.overview", nil); got != "Overview" {
		t.Fatalf("unsupported locale did not fall back to English: %q", got)
	}
}
