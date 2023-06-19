package source

// Contract defines the capabilities of a source provider.
type Contract interface {
	FetchCRD(dir string) (string, error)
}
