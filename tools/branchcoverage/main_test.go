package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShortCircuitAndNamedBooleanEvidence(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module github.com/yernsun/network-service-composer\n\ngo 1.27\n")
	write(".agents/rules/coverage.json", `{"backend":{"branches":99,"include":["internal/**/*.go","cmd/nsc-controller/**/*.go","cmd/nsc-agent/**/*.go"]}}`)
	if err := os.Mkdir(filepath.Join(root, "cmd"), 0700); err != nil {
		t.Fatal(err)
	}
	source := `package fixture
type Flag bool
const Stable = true && false
func next(v Flag, calls *int)Flag{*calls++;return v}
func And(a,b Flag,calls *int)Flag{return a && next(b,calls)}
func Or(a,b Flag,calls *int)Flag{return a || next(b,calls)}
func Named(a Flag)bool{if a{return true};return false}
func Constants(a Flag)Flag{return true && a || false}
`
	write("internal/fixture/fixture.go", source)
	write("tools/fixture/tool.go", "package tool\nfunc Choose(b bool)bool{if b{return true};return false}\n")
	for _, directory := range []string{"test", "tests", "testdata", "enginetest", "fixtures"} {
		write("internal/"+directory+"/helper.go", "package helper\nfunc Choose(b bool)bool{if b{return true};return false}\n")
	}
	write("internal/fixture/fixture_test.go", `package fixture
import("testing";tool "github.com/yernsun/network-service-composer/tools/fixture")
func TestBehavior(t *testing.T){
 calls:=0
 if And(false,true,&calls)||calls!=0{t.Fatal("AND evaluated its skipped operand")}
 if !Or(true,false,&calls)||calls!=0{t.Fatal("OR evaluated its skipped operand")}
 if And(true,false,&calls)||!And(true,true,&calls)||Or(false,false,&calls)||!Or(false,true,&calls)||calls!=4{t.Fatal("operand values or evaluation count changed")}
 if Named(false)||!Named(true)||tool.Choose(false)||!tool.Choose(true)||Stable{t.Fatal("named Boolean or tool behavior changed")}
 if Constants(false)||!Constants(true){t.Fatal("untyped constant conversion changed")}
}`)
	t.Chdir(root)
	if err := run([]string{"--output", "result.json"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("result.json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Total, Covered int
		Minimum        float64
		Sources        map[string]bool
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Total != 14 || report.Covered != 14 || report.Minimum != 99 || report.Sources["tools/fixture/tool.go"] {
		t.Fatalf("missing operand/tool evidence: %+v", report)
	}
	for _, directory := range []string{"test", "tests", "testdata", "enginetest", "fixtures"} {
		if report.Sources["internal/"+directory+"/helper.go"] {
			t.Fatalf("non-business fixture %s counted as service code", directory)
		}
	}
	after, err := os.ReadFile("internal/fixture/fixture.go")
	if err != nil || string(after) != source {
		t.Fatalf("source changed: %v", err)
	}
}

func TestRangeEvidencePreservesControlTransfersAndEvaluation(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module github.com/yernsun/network-service-composer\n\ngo 1.27\n")
	write(".agents/rules/coverage.json", `{"backend":{"include":["internal/**/*.go"]}}`)
	files := map[string]string{
		"normal":   `func Normal(xs []int)int{sum:=0;for _,v:=range xs{sum+=v};return sum}`,
		"zero":     `func Zero(xs []int)int{sum:=0;for _,v:=range xs{sum+=v};return sum}`,
		"break":    `func Break(xs []int)int{sum:=0;for _,v:=range xs{sum+=v;break};return sum}`,
		"continue": `func Continue(xs []int)int{sum:=0;for _,v:=range xs{sum+=v;continue};return sum}`,
		"label":    `func Label(xs []int)int{sum:=0;outer:for _,v:=range xs{for{sum+=v;break outer}};return sum}`,
		"nested":   `func Nested(xs []int)int{sum:=0;for _,v:=range xs{switch v{case 1:break;default:sum+=v}};return sum}`,
		"iterator": `func Iterator()int{sum:=0;seq:=func(yield func(int)bool){defer func(){sum+=100}();yield(2)};for v:=range seq{sum+=v;break};return sum}`,
		"array":    `func Array()int{var a *[3]int;sum:=0;for i:=range a{sum+=i};return sum}`,
		"integer":  `func Integer()int{sum:=0;for i:=range 3{sum+=i};return sum}`,
		"goto":     `func Goto()int{sum:=0;again:for i:=range 2{sum+=i};if sum==1{goto again};return sum}`,
		"constant": `func Constant()bool{return true || false}`,
	}
	for name, source := range files {
		write("internal/fixture/"+name+".go", "package fixture\n"+source+"\n")
	}
	write("internal/fixture/fixture_test.go", `package fixture
import "testing"
func TestRange(t *testing.T){
 if Normal([]int{1,2})!=3||Zero(nil)!=0||Break([]int{1,2})!=1||Continue([]int{1,2})!=3||Label([]int{1,2})!=1||Nested([]int{1,2})!=2||Iterator()!=102||Array()!=3||Integer()!=3||Goto()!=2||!Constant(){t.Fatal("range semantics changed")}
}`)
	t.Chdir(root)
	if err := run([]string{"--minimum", "0", "--output", "result.json"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("result.json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct{ Branches []Branch }
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, b := range report.Branches {
		if b.Kind != "range" {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(b.File), ".go")
		seen[name] = true
		wantIterate := name != "zero"
		wantExhausted := name != "break" && name != "label" && name != "iterator"
		if b.Hits["iterate"] != wantIterate || b.Hits["exhausted"] != wantExhausted {
			t.Fatalf("incorrect range evidence: %+v", b)
		}
	}
	if len(seen) != 10 {
		t.Fatalf("missing ranges: %v", seen)
	}
	for name, source := range files {
		after, err := os.ReadFile("internal/fixture/" + name + ".go")
		if err != nil || string(after) != "package fixture\n"+source+"\n" {
			t.Fatalf("source changed: %s %v", name, err)
		}
	}
}

func TestInvalidInstrumentationOptions(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"--minimum"}, {"--minimum", "NaN"}, {"--minimum", "+Inf"}, {"--minimum", "-1"}, {"--minimum", "101"}} {
		if err := run(args); err == nil {
			t.Fatalf("accepted invalid options %v", args)
		}
	}
}

// These fixtures assert the metric and preserve observable program behavior.
// They intentionally include multiple switch labels and an implicit default.
func TestDecisionInstrumentationAndFailClosedThreshold(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "branchcoverage")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tool: %v\n%s", err, output)
	}
	for _, tc := range []struct {
		name, tests     string
		unused          bool
		covered, total  int
		pass, testsPass bool
	}{
		{"all decisions", `check(t,0,"text",false,3);check(t,1,7,true,10);check(t,2,nil,false,6)`, false, 11, 11, true, true},
		{"missing decisions", `check(t,0,"text",false,3)`, false, 5, 11, false, true},
		{"untested source retained", `check(t,0,"text",false,3);check(t,1,7,true,10);check(t,2,nil,false,6)`, true, 11, 13, false, true},
		{"failed tests", `check(t,0,"text",false,999)`, false, 5, 11, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(path, body string) {
				t.Helper()
				target := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module github.com/yernsun/network-service-composer\n\ngo 1.27\n")
			write(".agents/rules/coverage.json", `{"backend":{"include":["internal/**/*.go","cmd/nsc-controller/**/*.go","cmd/nsc-agent/**/*.go"]}}`)
			if err := os.Mkdir(filepath.Join(root, "cmd"), 0700); err != nil {
				t.Fatal(err)
			}
			write("internal/fixture/fixture.go", `package fixture
func calculate(n int,v any,ch chan int)int{
 total:=0
 if n>0{total=1}
 for i:=0;i<n;i++{total++}
 switch n{case 0,1:total+=2;default:total+=3}
 switch v.(type){case string:total++;case int:total+=2}
 select{case x:=<-ch:total+=x;default:}
 return total
}`)
			write("internal/fixture/generated.go", "// Code generated by fixture. DO NOT EDIT.\npackage fixture\nfunc generated(b bool) int {if b{return 1};return 0}\n")
			if tc.unused {
				write("internal/unused/unused.go", "package unused\n// Code generated is mentioned in an ordinary comment, not a generator marker.\nfunc Unused(b bool)int{if b{return 1};return 0}\n")
			}
			write("internal/fixture/fixture_test.go", `package fixture
import "testing"
func check(t *testing.T,n int,v any,ready bool,want int){t.Helper();ch:=make(chan int,1);if ready{ch<-4};if got:=calculate(n,v,ch);got!=want{t.Fatalf("got %d want %d",got,want)}}
func TestFixture(t *testing.T){`+tc.tests+`}
`)
			report := filepath.Join(root, "report.json")
			command := exec.Command(binary, "--minimum", "98", "--output", report)
			command.Dir = root
			output, err := command.CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("threshold result %v\n%s", err, output)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Total, Covered int
				TestsPassed    bool `json:"tests_passed"`
			}
			if err = json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.Total != tc.total || result.Covered != tc.covered || result.TestsPassed != tc.testsPass {
				t.Fatalf("wrong inventory/evidence: %+v\n%s", result, output)
			}
			if !tc.testsPass && !strings.Contains(string(output), "instrumented tests failed") {
				t.Fatal("test failure was misreported as only a coverage failure")
			}
			// Overlay instrumentation must leave application source untouched.
			source, err := os.ReadFile(filepath.Join(root, "internal/fixture/fixture.go"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(source), "nscBranchProbe") {
				t.Fatal("tracked source was instrumented in place")
			}
		})
	}
}
