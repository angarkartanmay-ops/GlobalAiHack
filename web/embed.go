// Package web embeds the built dashboard.
package web

import "embed"

// Dist is the dashboard build output (web/dist).
//
//go:embed all:dist
var Dist embed.FS
