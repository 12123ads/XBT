package facephoto

const (
	// BaseVersionLegacy marks a stored base that may already have been
	// transformed before S21/OMP-52 introduced untouched official originals.
	// The default keeps every pre-existing row at this version.
	BaseVersionLegacy = 1
	// BaseVersionOriginal marks an official original stored exactly as
	// downloaded. A sign applies exactly one transform to it.
	BaseVersionOriginal = 2
)
