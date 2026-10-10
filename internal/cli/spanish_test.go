package cli

import (
	"strings"
	"testing"

	"conversor/internal/i18n"
)

func spanish(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	i18n.Set(i18n.Spanish)
}

func TestSpanishHelp(t *testing.T) {
	spanish(t)
	out, _, code := runCLI(t, "--help")
	for _, want := range []string{"Uso:", "Opciones:", "Comandos disponibles:", "Formato de destino", "Ayuda de conversor"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if code != 0 {
		t.Fatal(code)
	}
	if _, errOut, _ := runCLI(t, "--bogus"); !strings.Contains(errOut, "opción desconocida: --bogus") {
		t.Fatal(errOut)
	}
	if _, errOut, _ := runCLI(t, "targets"); !strings.Contains(errOut, "necesita 1 argumento(s)") {
		t.Fatal(errOut)
	}
}

func TestSpanishKeepsMachineOutputEnglish(t *testing.T) {
	spanish(t)
	out, _, _ := runCLI(t, "missing.txt", "--to", "webp", "--json")
	if !strings.Contains(out, `"kind":`) || strings.Contains(out, "no hay forma") {
		t.Fatalf("JSON messages stay English: %s", out)
	}
	i18n.Set(i18n.Spanish)
	skill, _, _ := runCLI(t, "skill")
	if !strings.Contains(skill, "List every known format") || strings.Contains(skill, "Lista todos") {
		t.Fatal("the skill is always English")
	}
}
