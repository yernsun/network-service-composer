package i18n

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLocaleOptionsPreserveConfiguredOrderAndNativeNames(t *testing.T) {
	var definitions []struct {
		Code Locale `json:"code"`
		Name string `json:"name"`
	}
	if e := json.Unmarshal(localeDefinition, &definitions); e != nil {
		t.Fatal(e)
	}
	options := localeOptions(localeDefinition)
	if len(options) != len(definitions) || !reflect.DeepEqual(options, Supported()) {
		t.Fatal("runtime languages diverged from locale authority")
	}
	for i, definition := range definitions {
		if options[i].Code != definition.Code || options[i].NativeName != definition.Name {
			t.Fatal("language order or native name changed", options[i])
		}
	}
	options[0].NativeName = "changed test copy"
	if Supported()[0].NativeName == options[0].NativeName {
		t.Fatal("decoded copy mutated global language options")
	}
	for _, raw := range []string{"[]", "null"} {
		if result := localeOptions([]byte(raw)); len(result) != 0 {
			t.Fatal("empty definitions synthesized language entries", result)
		}
	}
}

func TestMalformedLocaleDefinitionsStopInitialization(t *testing.T) {
	for _, raw := range []string{"{", `{"code":"en"}`, `[{"code":1,"name":"English"}]`, `[{"code":"en","name":true}]`} {
		t.Run(raw, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("malformed packaged locale definitions accepted")
				}
			}()
			localeOptions([]byte(raw))
		})
	}
}
