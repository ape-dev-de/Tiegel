package keymap

import (
	"runtime"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// coreSpecsErr records the registration of the keymap settings as core
// settings (decision D190/D210); checked by tests.
var coreSpecsErr = settings.RegisterCore(SettingSpecs(runtime.GOOS)...)
