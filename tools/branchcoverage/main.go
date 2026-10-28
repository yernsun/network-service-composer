// branchcoverage instruments application Go decisions through an overlay. It never rewrites source files.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Branch struct {
	File    string          `json:"file"`
	Line    int             `json:"line"`
	EndLine int             `json:"end_line"`
	Kind    string          `json:"kind"`
	Arms    []string        `json:"arms"`
	Hits    map[string]bool `json:"hits"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(arguments []string) error {
	flags := flag.NewFlagSet("branchcoverage", flag.ContinueOnError)
	minimum := flags.Float64("minimum", 0, "override aggregate Go branch minimum (default: repository coverage policy)")
	output := flags.String("output", "coverage/go-branches.json", "coverage JSON output")
	testPackages := flags.String("packages", "", "space-separated packages to test")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if math.IsNaN(*minimum) || math.IsInf(*minimum, 0) || *minimum < 0 || *minimum > 100 {
		return fmt.Errorf("minimum must be between 0 and 100")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "nsc-branches-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	overlay := map[string]string{}
	branches := map[string]*Branch{}
	sources := map[string]bool{}
	dirs := map[string]string{}
	fs := token.NewFileSet()
	policyBytes, err := os.ReadFile(".agents/rules/coverage.json")
	if err != nil {
		return err
	}
	var policy struct {
		Backend struct {
			Include  []string `json:"include"`
			Branches *float64 `json:"branches"`
		} `json:"backend"`
	}
	if err := json.Unmarshal(policyBytes, &policy); err != nil {
		return err
	}
	minimumProvided := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "minimum" {
			minimumProvided = true
		}
	})
	if !minimumProvided {
		if policy.Backend.Branches == nil || *policy.Backend.Branches <= 0 || *policy.Backend.Branches > 100 {
			return fmt.Errorf("backend branch threshold is missing or invalid")
		}
		*minimum = *policy.Backend.Branches
	}
	if len(policy.Backend.Include) == 0 {
		return fmt.Errorf("backend source scope is missing")
	}
	roots := []string{}
	patterns := []string{}
	for _, include := range policy.Backend.Include {
		if !strings.HasSuffix(include, "/**/*.go") {
			return fmt.Errorf("unsupported Go scope pattern: %s", include)
		}
		base := strings.TrimSuffix(include, "/**/*.go")
		if filepath.IsAbs(base) || strings.Contains(base, "..") {
			return fmt.Errorf("scope escapes repository: %s", include)
		}
		if _, e := os.Stat(base); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		roots = append(roots, base)
		patterns = append(patterns, "./"+base+"/...")
	}
	if len(roots) == 0 {
		return fmt.Errorf("no backend source roots exist")
	}
	if *testPackages == "" {
		*testPackages = strings.Join(patterns, " ")
	}
	loaded, err := packages.Load(&packages.Config{Mode: packages.LoadSyntax, Dir: root, Fset: fs}, patterns...)
	if err != nil {
		return err
	}
	typedFiles := map[string]*ast.File{}
	typeInfo := map[*ast.File]*types.Info{}
	for _, pkg := range loaded {
		if len(pkg.Errors) != 0 {
			return fmt.Errorf("load coverage source: %v", pkg.Errors)
		}
		for _, file := range pkg.Syntax {
			typedFiles[fs.Position(file.Pos()).Filename] = file
			typeInfo[file] = pkg.TypesInfo
		}
	}
	put := func(target string, data []byte) error {
		file := filepath.Join(temp, fmt.Sprintf("overlay-%d.go", len(overlay)))
		if e := os.WriteFile(file, data, 0600); e != nil {
			return e
		}
		overlay[target] = file
		return nil
	}
	for _, base := range roots {
		err = filepath.WalkDir(base, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				switch d.Name() {
				case "test", "tests", "testdata", "enginetest", "fixtures":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			file, e := parser.ParseFile(token.NewFileSet(), path, raw, parser.ParseComments)
			if e != nil {
				return e
			}
			if ast.IsGenerated(file) {
				return nil
			}
			absPath, e := filepath.Abs(path)
			if e != nil {
				return e
			}
			file = typedFiles[absPath]
			if file == nil {
				return fmt.Errorf("source not compiled for this platform; coverage needs an explicit platform run: %s", path)
			}
			sources[filepath.ToSlash(path)] = false
			absolute, _ := filepath.Abs(path)
			dirs[filepath.Dir(absolute)] = file.Name.Name
			makeID := func(node ast.Node, kind string, arms []string) string {
				id := fmt.Sprintf("%s:%d:%d:%s", filepath.ToSlash(path), fs.Position(node.Pos()).Line, fs.Position(node.Pos()).Column, kind)
				branches[id] = &Branch{File: filepath.ToSlash(path), Line: fs.Position(node.Pos()).Line, EndLine: fs.Position(node.End()).Line, Kind: kind, Arms: arms, Hits: map[string]bool{}}
				return id
			}
			literal := func(s string) ast.Expr { return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)} }
			hit := func(id, arm string) ast.Stmt {
				return &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("nscBranchProbe"), Sel: ast.NewIdent("Hit")}, Args: []ast.Expr{literal(id), literal(arm)}}}
			}
			wrap := func(condition ast.Expr, node ast.Node, kind string) ast.Expr {
				id := makeID(node, kind, []string{"true", "false"})
				return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("nscBranchProbe"), Sel: ast.NewIdent("Decision")}, Args: []ast.Expr{literal(id), condition}}
			}
			nodes := []ast.Node{}
			ast.Inspect(file, func(n ast.Node) bool {
				if declaration, ok := n.(*ast.GenDecl); ok && declaration.Tok == token.CONST {
					return false
				}
				switch n.(type) {
				case *ast.AssignStmt, *ast.ReturnStmt, *ast.ExprStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.GoStmt, *ast.DeferStmt, *ast.SendStmt, *ast.IncDecStmt, *ast.DeclStmt, *ast.BranchStmt:
					sources[filepath.ToSlash(path)] = true
				}
				if n != nil {
					nodes = append(nodes, n)
				}
				return true
			})
			parents := map[ast.Node]ast.Node{}
			stack := []ast.Node{}
			ast.Inspect(file, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return false
				}
				if len(stack) > 0 {
					parents[n] = stack[len(stack)-1]
				}
				stack = append(stack, n)
				return true
			})
			rangeEnds := map[*ast.RangeStmt]string{}
			rangeLabels := map[*ast.RangeStmt]string{}
			usedNames := map[string]bool{}
			for _, node := range nodes {
				if id, ok := node.(*ast.Ident); ok {
					usedNames[id.Name] = true
				}
			}
			changed := false
			for _, node := range nodes {
				switch n := node.(type) {
				case *ast.BinaryExpr:
					if n.Op == token.LAND || n.Op == token.LOR {
						// Constants are not runtime decisions and must retain their
						// contextual conversion to a named Boolean type.
						if typeInfo[file].Types[n.X].Value == nil {
							n.X = wrap(n.X, n.X, "short-circuit-left")
							changed = true
						}
						if typeInfo[file].Types[n.Y].Value == nil {
							n.Y = wrap(n.Y, n.Y, "short-circuit-right")
							changed = true
						}
					}
				case *ast.IfStmt:
					n.Cond = wrap(n.Cond, n, "if")
					changed = true
				case *ast.RangeStmt:
					id := makeID(n, "range", []string{"iterate", "exhausted"})
					rangeEnds[n] = id
					n.Body.List = append([]ast.Stmt{hit(id, "iterate")}, n.Body.List...)
					changed = true
				case *ast.ForStmt:
					if n.Cond != nil {
						n.Cond = wrap(n.Cond, n, "for")
						changed = true
					}
				case *ast.SwitchStmt:
					arms := []string{}
					hasDefault := false
					for i, s := range n.Body.List {
						c := s.(*ast.CaseClause)
						arm := strconv.Itoa(i)
						if c.List == nil {
							arm = "default"
							hasDefault = true
						}
						arms = append(arms, arm)
					}
					if !hasDefault {
						arms = append(arms, "default")
					}
					id := makeID(n, "switch", arms)
					for i, s := range n.Body.List {
						c := s.(*ast.CaseClause)
						c.Body = append([]ast.Stmt{hit(id, arms[i])}, c.Body...)
					}
					if !hasDefault {
						n.Body.List = append(n.Body.List, &ast.CaseClause{Body: []ast.Stmt{hit(id, "default")}})
					}
					changed = true
				case *ast.TypeSwitchStmt:
					arms := []string{}
					hasDefault := false
					for i, s := range n.Body.List {
						c := s.(*ast.CaseClause)
						arm := strconv.Itoa(i)
						if c.List == nil {
							arm = "default"
							hasDefault = true
						}
						arms = append(arms, arm)
					}
					if !hasDefault {
						arms = append(arms, "default")
					}
					id := makeID(n, "type-switch", arms)
					for i, s := range n.Body.List {
						c := s.(*ast.CaseClause)
						c.Body = append([]ast.Stmt{hit(id, arms[i])}, c.Body...)
					}
					if !hasDefault {
						n.Body.List = append(n.Body.List, &ast.CaseClause{Body: []ast.Stmt{hit(id, "default")}})
					}
					changed = true
				case *ast.SelectStmt:
					if len(n.Body.List) == 0 {
						continue
					}
					arms := []string{}
					for i := range n.Body.List {
						arms = append(arms, strconv.Itoa(i))
					}
					id := makeID(n, "select", arms)
					for i, s := range n.Body.List {
						c := s.(*ast.CommClause)
						c.Body = append([]ast.Stmt{hit(id, arms[i])}, c.Body...)
					}
					changed = true
				}
			}
			// A break exits the loop without observing exhaustion. Route it
			// past the exhaustion probe while preserving the loop's label,
			// range expression and compiler-managed iterator semantics.
			for _, node := range nodes {
				b, ok := node.(*ast.BranchStmt)
				if !ok || b.Tok != token.BREAK {
					continue
				}
				var target ast.Node
				for parent := parents[b]; parent != nil; parent = parents[parent] {
					if b.Label != nil {
						if label, ok := parent.(*ast.LabeledStmt); ok && label.Label.Name == b.Label.Name {
							target = label.Stmt
							break
						}
					} else {
						switch parent.(type) {
						case *ast.RangeStmt, *ast.ForStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
							target = parent
						}
						if target != nil {
							break
						}
					}
				}
				if loop, ok := target.(*ast.RangeStmt); ok {
					name := rangeLabels[loop]
					if name == "" {
						name = fmt.Sprintf("nscRangeEnd%d", loop.Pos())
						for usedNames[name] {
							name += "_"
						}
						usedNames[name] = true
						rangeLabels[loop] = name
					}
					b.Tok = token.GOTO
					b.Label = ast.NewIdent(name)
				}
			}
			usedLabels := map[types.Object]bool{}
			for _, node := range nodes {
				if branch, ok := node.(*ast.BranchStmt); ok && branch.Label != nil {
					if object := typeInfo[file].Uses[branch.Label]; object != nil {
						usedLabels[object] = true
					}
				}
			}
			var pruneUnusedLabel func(ast.Stmt) ast.Stmt
			pruneUnusedLabel = func(statement ast.Stmt) ast.Stmt {
				label, ok := statement.(*ast.LabeledStmt)
				if !ok {
					return statement
				}
				label.Stmt = pruneUnusedLabel(label.Stmt)
				if !usedLabels[typeInfo[file].Defs[label.Label]] {
					return label.Stmt
				}
				return label
			}
			insertEnds := func(statements []ast.Stmt) []ast.Stmt {
				result := []ast.Stmt{}
				for _, statement := range statements {
					statement = pruneUnusedLabel(statement)
					result = append(result, statement)
					unwrapped := statement
					for {
						label, ok := unwrapped.(*ast.LabeledStmt)
						if !ok {
							break
						}
						unwrapped = label.Stmt
					}
					if loop, ok := unwrapped.(*ast.RangeStmt); ok {
						result = append(result, hit(rangeEnds[loop], "exhausted"))
						if name := rangeLabels[loop]; name != "" {
							result = append(result, &ast.LabeledStmt{Label: ast.NewIdent(name), Stmt: &ast.EmptyStmt{Implicit: true}})
						}
					}
				}
				return result
			}
			for _, node := range nodes {
				switch n := node.(type) {
				case *ast.BlockStmt:
					n.List = insertEnds(n.List)
				case *ast.CaseClause:
					n.Body = insertEnds(n.Body)
				case *ast.CommClause:
					n.Body = insertEnds(n.Body)
				}
			}
			if !changed {
				return nil
			}
			file.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{&ast.ImportSpec{Name: ast.NewIdent("nscBranchProbe"), Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("github.com/yernsun/network-service-composer/internal/coverageprobe")}}}}}, file.Decls...)
			var out bytes.Buffer
			if e = format.Node(&out, fs, file); e != nil {
				return e
			}
			return put(absolute, out.Bytes())
		})
		if err != nil {
			return err
		}
	}
	probe := `package coverageprobe
import("encoding/json";"os";"path/filepath";"strconv";"sync")
var mu sync.Mutex
var hits=map[string]map[string]bool{}
func Hit(id,arm string){mu.Lock();defer mu.Unlock();if hits[id]==nil{hits[id]=map[string]bool{}};hits[id][arm]=true}
func Decision[T ~bool](id string,v T)T{Hit(id,strconv.FormatBool(bool(v)));return v}
func Flush()error{mu.Lock();defer mu.Unlock();data,err:=json.Marshal(hits);if err!=nil{return err};return os.WriteFile(filepath.Join(os.Getenv("NSC_BRANCH_RESULTS"),strconv.Itoa(os.Getpid())+".json"),data,0600)}
`
	if err = put(filepath.Join(root, "internal/coverageprobe/probe.go"), []byte(probe)); err != nil {
		return err
	}
	for dir, pkg := range dirs {
		src := fmt.Sprintf("package %s\nimport(\"fmt\";\"os\";\"testing\";p \"github.com/yernsun/network-service-composer/internal/coverageprobe\")\nfunc TestMain(m *testing.M){code:=m.Run();if err:=p.Flush();err!=nil{fmt.Fprintln(os.Stderr,err);code=1};os.Exit(code)}\n", pkg)
		if err = put(filepath.Join(dir, "zz_branchcoverage_test.go"), []byte(src)); err != nil {
			return err
		}
	}
	data, _ := json.Marshal(map[string]any{"Replace": overlay})
	overlayPath := filepath.Join(temp, "overlay.json")
	if err = os.WriteFile(overlayPath, data, 0600); err != nil {
		return err
	}
	resultDir := filepath.Join(temp, "results")
	if err = os.Mkdir(resultDir, 0700); err != nil {
		return err
	}
	args := append([]string{"test", "-vet=off", "-count=1", "-overlay", overlayPath}, strings.Fields(*testPackages)...)
	args = append(args, flags.Args()...)
	goCommand := os.Getenv("GO")
	if goCommand == "" {
		goCommand = "go"
	}
	command := exec.Command(goCommand, args...)
	command.Env = append(os.Environ(), "NSC_BRANCH_RESULTS="+resultDir)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	testErr := command.Run()
	files, err := os.ReadDir(resultDir)
	if err != nil {
		return err
	}
	for _, f := range files {
		b, e := os.ReadFile(filepath.Join(resultDir, f.Name()))
		if e != nil {
			return e
		}
		var hits map[string]map[string]bool
		if e = json.Unmarshal(b, &hits); e != nil {
			return e
		}
		for id, arms := range hits {
			if branch := branches[id]; branch != nil {
				for arm := range arms {
					branch.Hits[arm] = true
				}
			}
		}
	}
	total, covered := 0, 0
	keys := []string{}
	for id, b := range branches {
		keys = append(keys, id)
		for _, arm := range b.Arms {
			total++
			if b.Hits[arm] {
				covered++
			}
		}
	}
	sort.Strings(keys)
	ordered := make([]*Branch, 0, len(keys))
	for _, id := range keys {
		ordered = append(ordered, branches[id])
	}
	percent := 100.0
	if total > 0 {
		percent = 100 * float64(covered) / float64(total)
	}
	report := map[string]any{"metric": "Go AST decisions (including range iterate/exhausted) and runtime short-circuit operand outcomes", "scope": policy.Backend.Include, "excluded": []string{"generated Go", "test sources", "test/testdata/enginetest/fixtures directories (same classification as incremental coverage)", "coverage probe"}, "total": total, "covered": covered, "percent": percent, "minimum": *minimum, "tests_passed": testErr == nil, "sources": sources, "branches": ordered}
	b, _ := json.MarshalIndent(report, "", "  ")
	if err = os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
		return err
	}
	if err = os.WriteFile(*output, b, 0644); err != nil {
		return err
	}
	fmt.Printf("Go decision branches: %d/%d (%.2f%%), required %.2f%%\n", covered, total, percent, *minimum)
	if testErr != nil {
		return fmt.Errorf("instrumented tests failed: %w", testErr)
	}
	if total == 0 || len(files) == 0 {
		return fmt.Errorf("no branch inventory or test evidence was produced")
	}
	if percent < *minimum {
		return fmt.Errorf("branch coverage below threshold")
	}
	return nil
}
