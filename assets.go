// Package lasta embeds the templates and static assets so the compiled
// binary is fully self-contained. go:embed paths are relative to this file,
// which is why the embed lives at the repo root next to those directories.
package lasta

import "embed"

//go:embed templates static
var Assets embed.FS
