// Package web embeds templates, string catalogues, public static assets and the compiled
// Svelte application (built into web/app by `npm run build` in frontend/).
package web

import "embed"

// FS holds every embedded web asset.
//
//go:embed templates i18n static all:app
var FS embed.FS
