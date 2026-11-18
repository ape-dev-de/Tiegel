package phpruntime

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

// The settings import writes the keys of this module by name (D394).
func TestIDEImportKeys(t *testing.T) {
	if ideimport.KeyMappings != SettingMappings || ideimport.KeyExecMode != SettingExecMode ||
		ideimport.KeyDockerImages != SettingImages || ideimport.KeyInterpreter != SettingInterpreter {
		t.Error("ideimport keys differ from php-runtime's")
	}
}
