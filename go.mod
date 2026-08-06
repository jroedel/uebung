module github.com/jroedel/uebung

// Targeting Go 1.24 because this build sandbox cannot download the 1.26
// toolchain. The sibling project (adb-broker) uses 1.26; bump this line and the
// CI image together when moving there. Nothing here relies on a <1.26 feature
// that would break on 1.26.
go 1.24
