package typed

import (
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/internal/mmap"
)

// mapInput is mmap.Map for a name ssql.OpenInput accepts: a URL's body
// is fetched into memory instead, the size the mapping would have been,
// so the parallel readers shard a URL the way they shard a file.
func mapInput(name string) (*mmap.Mapped, error) {
	if ssql.IsHTTPURL(name) {
		data, err := ssql.ReadInput(name)
		if err != nil {
			return nil, err
		}
		return &mmap.Mapped{Data: data}, nil
	}
	return mmap.Map(name)
}
