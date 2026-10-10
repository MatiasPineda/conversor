package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// messages finds every message passed to T and N in the repository's Go
// files, which must be string literals so this test can see them.
func messages(t *testing.T) map[string]string {
	t.Helper()
	found := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "dist" || strings.HasPrefix(d.Name(), ".")) && path != "../.." {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "i18n" {
				return true
			}
			var msgArgs []ast.Expr
			switch sel.Sel.Name {
			case "T":
				msgArgs = call.Args[:1]
			case "N":
				msgArgs = call.Args[1:3]
			default:
				return true
			}
			for _, a := range msgArgs {
				lit, ok := a.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Errorf("%s: the message must be a string literal", fset.Position(a.Pos()))
					continue
				}
				s, _ := strconv.Unquote(lit.Value)
				found[s] = fset.Position(a.Pos()).String()
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

var verb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

func verbs(s string) []string {
	var out []string
	for _, v := range verb.FindAllString(s, -1) {
		if v != "%%" {
			out = append(out, v)
		}
	}
	return out
}

func TestSpanishCatalogIsComplete(t *testing.T) {
	used := messages(t)
	if len(used) < 100 {
		t.Fatalf("found only %d messages; is the walk broken?", len(used))
	}
	var missing []string
	for msg, at := range used {
		es, ok := es[msg]
		if !ok {
			missing = append(missing, at+": "+strconv.Quote(msg))
			continue
		}
		if a, b := verbs(msg), verbs(es); !slices.Equal(a, b) {
			t.Errorf("%s: %q has placeholders %v, its translation %v", at, msg, a, b)
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("no Spanish for %s", m)
	}
	for msg := range es {
		if _, ok := used[msg]; !ok {
			t.Errorf("unused translation %q", msg)
		}
	}
}

func TestMatch(t *testing.T) {
	for in, want := range map[string]string{
		"en_US.UTF-8": English, "en-GB": English, "en": English, "EN_us": English,
		"es_CL.UTF-8": Spanish, "es-419": Spanish, "fr_FR": Spanish, "": Spanish, "C.UTF-8": Spanish, "eng": Spanish,
	} {
		if got := Match(in); got != want {
			t.Errorf("Match(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnvironmentOrder(t *testing.T) {
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "C")
	t.Setenv("LANG", "en_US.UTF-8")
	if got := fromEnv(); got != "en_US.UTF-8" {
		t.Fatalf("C names no language, so LANG decides: got %q", got)
	}
	t.Setenv("LANGUAGE", "es_CL:en")
	if got := fromEnv(); got != "es_CL" {
		t.Fatalf("LANGUAGE comes first: got %q", got)
	}
	t.Setenv("CONVERSOR_LANG", "en")
	if Detect() != English {
		t.Fatal("CONVERSOR_LANG overrides the system")
	}
}

func TestSpanishFormatting(t *testing.T) {
	defer Set(Lang())
	Set(Spanish)
	if got := N(3, "%d file", "%d files"); got != "3 archivos" {
		t.Fatal(got)
	}
	if got := N(1, "%d file", "%d files"); got != "1 archivo" {
		t.Fatal(got)
	}
	if got := Decimal(1.25, 1); got != "1,2" && got != "1,3" {
		t.Fatal(got)
	}
	at := time.Date(2026, 9, 28, 16, 8, 0, 0, time.UTC)
	if Clock(at) != "16:08" || Date(at, false) != "28 sept" || Date(at, true) != "28 sept 2026" {
		t.Fatal(Clock(at), Date(at, false), Date(at, true))
	}
	Set(English)
	if Clock(at) != "4:08 PM" || Date(at, true) != "Sep 28, 2026" || T("Yesterday") != "Yesterday" {
		t.Fatal(Clock(at), Date(at, true))
	}
}
