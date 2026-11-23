package compose

import "github.com/sirupsen/logrus"

// compose-go logs warnings such as `The "X" variable is not set` through
// logrus' standard logger straight to stderr. Unset variables are normal in
// development compose files (they come from a shell or .env the IDE does
// not see) and the warnings cluttered headless output and GUI logs, so only
// errors are logged. logrus has no other user in Tiegel.
func init() { logrus.SetLevel(logrus.ErrorLevel) }
