//go:build !ladybug

package knowledge

// NewConfiguredStore returns the contract reference implementation for normal
// builds and tests. The Kind image is compiled with the ladybug build tag.
func NewConfiguredStore(pack DomainPack, _ string) (KnowledgeStore, error) {
	return NewMemoryStore(pack)
}

func StoreProviderName() string { return "memory-reference" }
