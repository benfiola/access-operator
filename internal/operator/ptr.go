package operator

// Returns a pointer to [v].
// This primarily exists to simplify passing pointers to literals.
func ptr[T any](v T) *T {
	return &v
}
