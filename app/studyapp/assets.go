package studyapp

import (
	"embed"
	"io/fs"
	"mime"
)

//go:embed static
var staticFiles embed.FS

// Go's mime table has no entry for .webmanifest, and http.FileServer falls back
// to sniffing, which lands on text/plain or application/octet-stream. Browsers
// reject a manifest served under either, so the install prompt silently never
// appears. Registering it here keeps the fix next to the files it applies to.
func init() {
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		panic(err) // a bad literal extension is a build mistake, not a runtime case.
	}
}

// Assets returns the embedded browser client as a filesystem rooted so that
// index.html sits at "/". Serving it from the binary means the app has no static
// files to deploy alongside it.
func Assets() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		// The embed path is a compile-time constant, so a failure here is a build
		// mistake, not a runtime condition worth returning.
		panic(err)
	}

	return sub
}
