// Package ui embeds the built Control Plane frontend (ui/dist) into the
// engine binary so a single artifact serves both API and SPA.
package ui

import "embed"

// DistFS contains the Vite production build output. A minimal
// ui/dist/index.html is committed so builds and tests work before
// `npm run build` has been executed.
//
//go:embed all:dist
var DistFS embed.FS
