package application

import "testing"

func TestInterpolationPreservesLiteralDollarAndIdentifierBoundaries(t *testing.T) {
	env := map[string]string{"_": "underscore", "A0_Za9": "mixed", "a": "lower", "Z": "upper"}
	for _, tc := range []struct{ input, want string }{
		{"$", "$"}, {"$_", "underscore"}, {"$A0_Za9", "mixed"}, {"$a[", "lower["}, {"$Z`", "upper`"}, {"$[", "$["}, {"$`", "$`"}, {"$!", "$!"}, {"$~", "$~"},
		{"${MISSING:-$}", "$"}, {"${MISSING:-$a}", "lower"}, {"${MISSING:-${OTHER:-$_}}", "underscore"}, {"${MISSING:-x$!y}", "x$!y"},
	} {
		got, e := Interpolate(tc.input, env)
		if e != nil || got != tc.want {
			t.Fatalf("%q resolved to %q, %v; want %q", tc.input, got, e, tc.want)
		}
	}
	if got, e := Interpolate("${MISSING:-$", env); e == nil || got != "" {
		t.Fatal("unterminated nested operand exposed partial configuration", got, e)
	}
}
