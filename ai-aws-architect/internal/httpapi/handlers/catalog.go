package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cloud-ai/ai-aws-architect/internal/catalog"
	"github.com/cloud-ai/ai-aws-architect/internal/reasoning"
	"github.com/cloud-ai/ai-aws-architect/internal/runner"
)

type CatalogHandler struct {
	catalog   *catalog.Catalog
	runner    runner.Runner
	reasoning *reasoning.Service
}

func NewCatalogHandler(c *catalog.Catalog, r runner.Runner, reasoningSvc *reasoning.Service) *CatalogHandler {
	return &CatalogHandler{catalog: c, runner: r, reasoning: reasoningSvc}
}

// catalogEntry is a template plus whether the active runner can actually build
// it. Embedded so the template's own fields stay at the top level and a client
// sees one flat object rather than a wrapper it has to unwrap.
type catalogEntry struct {
	catalog.Template
	// Provisionable is false when a template can be proposed and approved but
	// would fail at plan time. Depends on the RUNNER, not the catalog: under
	// the stub everything is provisionable.
	Provisionable bool `json:"provisionable"`
	// UnprovisionableReason is empty when Provisionable is true.
	UnprovisionableReason string `json:"unprovisionable_reason"`
}

// List exposes the vetted blocks so the UI can name templates, show parameter
// ranges and explain why something is out of scope, without duplicating the
// catalog on the frontend.
//
// Each entry carries whether it can actually be provisioned right now. Without
// it a client has to keep its own list of module-less templates, which is a
// second source of truth for a fact the server already knows. The failure
// mode of getting it wrong is a plan that dies after the user approved it.
func (h *CatalogHandler) List(c *gin.Context) {
	templates := h.catalog.All()
	entries := make([]catalogEntry, 0, len(templates))

	for _, t := range templates {
		e := catalogEntry{Template: t, Provisionable: h.runner.Provisionable(t.ID)}
		if !e.Provisionable {
			e.UnprovisionableReason = "no infrastructure module is bundled for this template, " +
				"so it can be proposed but not built"
		}
		entries = append(entries, e)
	}

	engine, model := h.reasoning.Engine()

	c.JSON(http.StatusOK, gin.H{
		"templates": entries,
		// Both stubs are named so a client can say "simulated" rather than
		// guessing. runner is inferable from the provisionable flags being all
		// true; reasoning_engine is not inferable from anything, which is why
		// it has to be stated.
		"runner":           h.runner.Name(),
		"reasoning_engine": engine,
		"reasoning_model":  model,
	})
}
