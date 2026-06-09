// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package build holds compile-time metadata and project credits.
package build

import "fmt"

const (
	Name      = "Hydra"
	Version   = "0.1.0"
	Author    = "Elchi"
	Copyright = "Copyright (c) 2026 Elchi. All rights reserved."
	Tagline   = "One stream in, every platform out."
)

// Credit is the short attribution shown across interfaces.
const Credit = "Made by Elchi"

// UserAgent identifies Hydra to upstream RTMP/HTTP services.
func UserAgent() string {
	return fmt.Sprintf("%s/%s", Name, Version)
}

// VersionLine is a one-line version string for CLI output.
func VersionLine() string {
	return fmt.Sprintf("%s %s  %s", Name, Version, Credit)
}
