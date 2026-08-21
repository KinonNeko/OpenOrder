package memstore_test

import (
	"testing"

	"github.com/opendiscord/opendiscord/internal/store"
	"github.com/opendiscord/opendiscord/internal/store/memstore"
	"github.com/opendiscord/opendiscord/internal/store/storetest"
)

func TestMemstoreConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return memstore.New() })
}
