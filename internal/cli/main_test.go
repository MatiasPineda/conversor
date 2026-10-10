package cli

import (
	"os"
	"testing"

	"conversor/internal/i18n"
)

// The tests check English messages; i18n has the Spanish ones.
func TestMain(m *testing.M) {
	i18n.Set(i18n.English)
	os.Exit(m.Run())
}
