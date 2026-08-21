package memstore_test

import (
	"testing"

	"github.com/KinonNeko/openorder/internal/store"
	"github.com/KinonNeko/openorder/internal/store/memstore"
	"github.com/KinonNeko/openorder/internal/store/storetest"
)

func TestMemstoreConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return memstore.New() })
}
