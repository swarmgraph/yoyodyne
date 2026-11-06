package config

// Team is whether more than one factory works this repository through its
// shared remote (docs/designs/working-alongside-other-contributors.md,
// section 5). It is kept in the committed configuration so every factory
// working the repository reads the same value.
//
// With it off, which is the default, the harness works the repository alone:
// it takes no claim on the remote, and no start of new work waits on the
// remote. With it on, every fresh start first takes the item's claim reference
// on the shared remote, and `yoyo doctor` refuses the setting where the forge
// will not hold those references or publishing is off.
//
// Turning it on is the Lead Product Manager's decision and the development
// manager applies it. It selects whether the harness coordinates through the
// remote and grants nothing: the claims are pushed by the harness alone.
type Team struct {
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

type teamDocument struct {
	Enabled *bool `yaml:"enabled"`
}
