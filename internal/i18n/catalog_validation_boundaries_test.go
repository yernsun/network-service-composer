package i18n

import (
	"context"
	"reflect"
	"testing"
)

func TestCatalogValidationRejectsMissingKeysAndChangedPlaceholders(t *testing.T) {
	for _, mode := range []string{"valid", "missing English", "empty English", "blank key", "blank text", "missing locale", "short locale", "wrong key", "empty translation", "different placeholders"} {
		t.Run(mode, func(t *testing.T) {
			values := map[Locale]map[string]string{}
			for _, o := range options {
				values[o.Code] = map[string]string{"greeting": "Hello {name}, {name}: {count}", "plain": "Text"}
			}
			other := options[1].Code
			switch mode {
			case "missing English":
				delete(values, English)
			case "empty English":
				values[English] = map[string]string{}
			case "blank key":
				values[English][" "] = "text"
			case "blank text":
				values[English]["plain"] = " \t"
			case "missing locale":
				delete(values, other)
			case "short locale":
				delete(values[other], "plain")
			case "wrong key":
				delete(values[other], "plain")
				values[other]["replacement"] = "text"
			case "empty translation":
				values[other]["plain"] = " "
			case "different placeholders":
				values[other]["greeting"] = "Hello {other}"
			}
			e := validateCatalogs(values)
			if (e == nil) != (mode == "valid") {
				t.Fatal("catalog compatibility was misclassified", e)
			}
		})
	}
	if got := placeholders("{z} {name} {z} {name}"); !reflect.DeepEqual(got, []string{"name", "z"}) {
		t.Fatal("duplicate placeholders changed contract", got)
	}
	if got := interpolate("Hello {name}: {pending}", map[string]any{"name": "reader"}); got != "Hello reader: {pending}" {
		t.Fatal("missing parameter was fabricated", got)
	}
}
func TestUnknownLocaleFallsBackWithoutCreatingUnsupportedCatalog(t *testing.T) {
	unknown := Locale("unsupported")
	if Has(unknown, "key") {
		t.Fatal("unknown locale claimed a key")
	}
	if raw, ok := CatalogJSON(unknown); ok || raw != nil {
		t.Fatal("unknown catalog reported available")
	}
	if got := FromContext(WithLocale(context.Background(), unknown)); got != English {
		t.Fatal("unknown locale not normalized", got)
	}
	if got := FromContext(context.WithValue(context.Background(), localeContextKey{}, unknown)); got != English {
		t.Fatal("foreign context locale accepted", got)
	}
	if got := FromContext(context.WithValue(context.Background(), localeContextKey{}, 17)); got != English {
		t.Fatal("wrong context type accepted", got)
	}
	for _, v := range []string{"xx-Unknown", "not_a_language_!"} {
		if got, ok := Parse(v); ok {
			t.Errorf("unsupported locale selected %s for %q", got, v)
		}
	}
}
