package metadata_test

import (
	"testing"

	"github.com/gabriwar/anyscale/internal/metadata"
	"github.com/stretchr/testify/require"
)

func TestReleaseNotes(t *testing.T) {
	rnv, rn := metadata.ReleaseNotes()
	require.NotEmpty(t, rnv)
	require.NotEmpty(t, rn)
	require.Regexp(t, `^v[0-9]+\.[0-9]+\.[0-9]+$`, rnv)
	require.Regexp(t, `^<ul>`, rn)
}
