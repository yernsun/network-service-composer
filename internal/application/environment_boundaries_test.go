package application

import (
	"reflect"
	"strings"
	"testing"
)

func TestInterpolationOperatorPresenceAndEmptyValues(t *testing.T) {
	for _, tc := range []struct {
		expression, absent, empty, present string
		absentError, emptyError            bool
	}{
		{"${KEY}", "", "", "value", false, false},
		{"${KEY:-fallback}", "fallback", "fallback", "value", false, false},
		{"${KEY-fallback}", "fallback", "", "value", false, false},
		{"${KEY:+alternative}", "", "", "alternative", false, false},
		{"${KEY+alternative}", "", "alternative", "alternative", false, false},
		{"${KEY:?required}", "required", "required", "value", true, true},
		{"${KEY?required}", "required", "", "value", true, false},
		{"${KEY:?}", "KEY is required", "KEY is required", "value", true, true},
		{"${KEY?}", "KEY is required", "", "value", true, false},
	} {
		for _, state := range []struct {
			name    string
			values  map[string]string
			want    string
			failure bool
		}{
			{"absent", nil, tc.absent, tc.absentError}, {"empty", map[string]string{"KEY": ""}, tc.empty, tc.emptyError}, {"present", map[string]string{"KEY": "value"}, tc.present, false},
		} {
			t.Run(tc.expression+"/"+state.name, func(t *testing.T) {
				got, err := Interpolate(tc.expression, state.values)
				if state.failure {
					if err == nil || err.Error() != state.want || got != "" {
						t.Fatalf("got %q, %v", got, err)
					}
					return
				}
				if err != nil || got != state.want {
					t.Fatalf("got %q, %v; want %q", got, err, state.want)
				}
			})
		}
	}
	for _, tc := range []struct{ input, want string }{
		{"${MISSING:-${OTHER:-nested}}", "nested"}, {"$KEY/$a1_/$Z/$9/$/$.", "value/lower/upper/$9/$/$."}, {"$$KEY $$$KEY", "$KEY $value"},
	} {
		got, err := Interpolate(tc.input, map[string]string{"KEY": "value", "a1_": "lower", "Z": "upper"})
		if err != nil || got != tc.want {
			t.Fatalf("%q => %q %v", tc.input, got, err)
		}
	}
	for _, input := range []string{"${", "${KEY", "${KEY:-${OTHER}", "${9BAD}", "${BAD.KEY}", "${}"} {
		if got, err := Interpolate(input, nil); err == nil || got != "" {
			t.Fatalf("invalid interpolation %q accepted: %q %v", input, got, err)
		}
	}
}

func TestDotEnvMalformedInputAndCanonicalRoundTrip(t *testing.T) {
	for _, raw := range []string{"NO_ASSIGNMENT", "=value", "9BAD=value", "A=one\nA=two", "A='unfinished", "A=\"unfinished", "A='closed'trailing", "A=\"closed\"trailing", `A="bad\q"`, "A=${MISSING:?required}", "A=" + strings.Repeat("x", 4<<20)} {
		if values, err := ParseDotEnv(raw); err == nil || values != nil {
			t.Fatalf("invalid dotenv accepted (length %d): %v", len(raw), err)
		}
	}
	values := map[string]string{"EMPTY": "", "UNICODE": "你好", "LITERAL": "$HOST ${SECRET} $$", "MULTILINE": "line one\nline two\t\"quoted\"", "QUOTE": "it's a backslash \\"}
	got, err := ParseDotEnv(RenderDotEnv(values))
	if err != nil || !reflect.DeepEqual(got, values) {
		t.Fatalf("canonical round trip: %#v %v", got, err)
	}
	got, err = ParseDotEnv("# header\r\nexport A='one\ntwo' # comment\rB=hash#literal\rC=value # comment\nD='it\\'s'\nE=\"quote\\\"inside\"\n")
	want := map[string]string{"A": "one\ntwo", "B": "hash#literal", "C": "value", "D": "it's", "E": "quote\"inside"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("quoted input: %#v %v", got, err)
	}
}

func TestEnvironmentPatchErrorsNeverMutateInputMaps(t *testing.T) {
	empty := ""
	value := "new"
	raw := "A=raw"
	previous := map[string]string{"A": "old", "SECRET": "saved"}
	defaults := map[string]string{"DEFAULT": "default"}
	for _, items := range [][]EnvironmentVariable{
		{{Key: "BAD-KEY", Value: &value}}, {{Key: "A", Value: &value}, {Key: " A ", Value: &value}}, {{Key: "ABSENT"}},
	} {
		if got, _, err := NormalizeEnvironment(EnvironmentInput{Values: &items}, defaults, previous); err == nil || got != nil {
			t.Fatalf("invalid patch accepted: %#v %v", got, err)
		}
	}
	items := []EnvironmentVariable{{Key: "A", Value: &value}}
	if _, _, err := NormalizeEnvironment(EnvironmentInput{Raw: &raw, Values: &items}, defaults, previous); err == nil {
		t.Fatal("mutually exclusive modes accepted")
	}
	bad := "invalid"
	if _, _, err := NormalizeEnvironment(EnvironmentInput{Raw: &bad}, defaults, previous); err == nil {
		t.Fatal("invalid raw mode accepted")
	}
	items = []EnvironmentVariable{{Key: "SECRET", Secret: true, Value: &empty}, {Key: "A", Clear: true}, {Key: "NEW_SECRET", Secret: true, Value: &empty}}
	got, secrets, err := NormalizeEnvironment(EnvironmentInput{Values: &items}, defaults, previous)
	if err != nil || !reflect.DeepEqual(got, map[string]string{"SECRET": "saved", "NEW_SECRET": "", "DEFAULT": "default"}) || !reflect.DeepEqual(secrets, []string{"SECRET", "NEW_SECRET"}) {
		t.Fatalf("patch result %#v %v %v", got, secrets, err)
	}
	if previous["A"] != "old" || previous["SECRET"] != "saved" || len(defaults) != 1 {
		t.Fatal("input maps mutated")
	}
	for _, tc := range []struct {
		raw         string
		items       []EnvironmentVariable
		equal, fail bool
	}{
		{"A=new", []EnvironmentVariable{{Key: "A", Value: &value}}, true, false}, {"A=other", items, false, false}, {"bad", nil, false, true}, {"", []EnvironmentVariable{{Key: "INVALID-KEY"}}, false, true},
	} {
		equal, err := EqualEnvironmentModes(tc.raw, tc.items)
		if (err != nil) != tc.fail || equal != tc.equal {
			t.Fatalf("mode equivalence: %t %v", equal, err)
		}
	}
	if got := RedactResolvedYAML("${MISSING:?required}", previous, []string{"SECRET", "ABSENT"}); got != "[redacted preview unavailable]" {
		t.Fatalf("unsafe fallback: %q", got)
	}
}
