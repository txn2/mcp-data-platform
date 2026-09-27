package admin

import (
	"github.com/txn2/mcp-data-platform/internal/admin/memoryapi"
)

// MemoryLister reads memory records by filter for the admin memory list.
// Satisfied by memory.Store.
type MemoryLister = memoryapi.Lister

// registerMemoryRoutes mounts the admin memory list, implemented in the
// memoryapi subpackage. Without a memory store the route stays off.
func (h *Handler) registerMemoryRoutes() {
	memoryapi.Register(h.mux, memoryapi.Config{Records: h.deps.MemoryRecords})
}
