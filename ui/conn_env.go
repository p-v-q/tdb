package ui

// conn_env.go resolves a connection's password from the environment.
//
// A password can come from two places: the stored Password field, or an
// environment variable named by PasswordEnv. The variable wins when it is set,
// and its value never reaches the settings document — the resolved password is
// used only while building the driver configuration (see driverConfig and
// composeDSN), so the settings file stays free of secrets.

import (
	"fmt"
	"os"

	"github.com/p-v-q/tdb/settings"
)

// resolveConnectionPassword returns the password to use for c.
//
// When PasswordEnv is empty the stored password is used as-is. Otherwise the
// named variable must exist and be non-empty; an unset or empty variable is an
// error naming the variable, so the caller can tell the user which one to set
// (the message itself is localized by the caller). c is never modified.
func resolveConnectionPassword(c settings.Connection) (string, error) {
	if c.PasswordEnv == "" {
		return c.Password, nil
	}
	value, ok := os.LookupEnv(c.PasswordEnv)
	if !ok || value == "" {
		return "", fmt.Errorf("environment variable %s is not set", c.PasswordEnv)
	}
	return value, nil
}
