package splice

import (
	"fmt"
	"sync"

	"github.com/wyw14/solo-038/internal/model"
)

// ProductionRoute is the downstream order a seam conclusion travels after the
// splice table: printing units, dryer, then the folder delivery.
var ProductionRoute = []string{"press", "dryer", "folder"}

// SeamCheckpoint marks one station the seam conclusion has passed on the web path.
type SeamCheckpoint struct {
	Segment  string  `json:"segment"`
	Position float64 `json:"position"`
}

// SeamRoute is the trace of one certificate travelling the web path.
type SeamRoute struct {
	CertificateID model.EventID    `json:"certificate_id"`
	SeamID        string           `json:"seam_id"`
	Checkpoints   []SeamCheckpoint `json:"checkpoints"`
	Delivered     bool             `json:"delivered"`
}

// SeamRouter tracks where each seam conclusion currently sits on the web path.
// A conclusion only becomes visible to the folder after it has passed every
// production segment in order — before that it is "not traceable downstream".
type SeamRouter struct {
	mu     sync.Mutex
	route  []string
	routes map[model.EventID]*SeamRoute
}

func NewSeamRouter(route []string) (*SeamRouter, error) {
	if len(route) == 0 {
		return nil, fmt.Errorf("seam route requires at least one segment")
	}
	seen := map[string]bool{}
	for _, segment := range route {
		if segment == "" || seen[segment] {
			return nil, fmt.Errorf("seam route segment is invalid")
		}
		seen[segment] = true
	}
	return &SeamRouter{route: append([]string(nil), route...), routes: map[model.EventID]*SeamRoute{}}, nil
}

// Reset clears every trace before rebuilding from the journal.
func (router *SeamRouter) Reset() {
	router.mu.Lock()
	defer router.mu.Unlock()
	router.routes = map[model.EventID]*SeamRoute{}
}

// Enter registers a freshly inspected certificate at the splice table.
func (router *SeamRouter) Enter(certificate model.SeamCertificate) error {
	if err := certificate.Validate(); err != nil {
		return err
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if _, ok := router.routes[certificate.ID]; ok {
		return fmt.Errorf("seam certificate %s is already on the web path", certificate.ID)
	}
	router.routes[certificate.ID] = &SeamRoute{
		CertificateID: certificate.ID,
		SeamID:        certificate.SeamID,
		Checkpoints:   []SeamCheckpoint{{Segment: "splice", Position: certificate.Meter}},
	}
	return nil
}

// Pass records that the certificate's paper has moved through one more segment.
// Segments must be passed in route order; skipping ahead is rejected.
func (router *SeamRouter) Pass(certificateID model.EventID, segment string, position float64) error {
	router.mu.Lock()
	defer router.mu.Unlock()
	route, ok := router.routes[certificateID]
	if !ok {
		return fmt.Errorf("seam certificate %s is not on the web path", certificateID)
	}
	next := len(route.Checkpoints) - 1 // checkpoints[0] is the splice table
	if next >= len(router.route) {
		return fmt.Errorf("seam certificate %s already reached the folder", certificateID)
	}
	if segment != router.route[next] {
		return fmt.Errorf("seam must pass %s before %s", router.route[next], segment)
	}
	if position < 0 {
		return fmt.Errorf("seam checkpoint position is invalid")
	}
	route.Checkpoints = append(route.Checkpoints, SeamCheckpoint{Segment: segment, Position: position})
	return nil
}

// Route returns a copy of one certificate's trace.
func (router *SeamRouter) Route(certificateID model.EventID) (SeamRoute, bool) {
	router.mu.Lock()
	defer router.mu.Unlock()
	route, ok := router.routes[certificateID]
	if !ok {
		return SeamRoute{}, false
	}
	return SeamRoute{CertificateID: route.CertificateID, SeamID: route.SeamID, Checkpoints: append([]SeamCheckpoint(nil), route.Checkpoints...), Delivered: route.Delivered}, true
}

// ArrivedAtFolder reports whether the conclusion has passed every production
// segment and may now be handed to the folder.
func (router *SeamRouter) ArrivedAtFolder(certificateID model.EventID) bool {
	router.mu.Lock()
	defer router.mu.Unlock()
	route, ok := router.routes[certificateID]
	return ok && len(route.Checkpoints) == len(router.route)+1
}

// MarkDelivered latches the hand-off to the folder; it is idempotent.
func (router *SeamRouter) MarkDelivered(certificateID model.EventID) error {
	router.mu.Lock()
	defer router.mu.Unlock()
	route, ok := router.routes[certificateID]
	if !ok {
		return fmt.Errorf("seam certificate %s is not on the web path", certificateID)
	}
	if len(route.Checkpoints) != len(router.route)+1 {
		return fmt.Errorf("seam certificate %s has not reached the folder", certificateID)
	}
	route.Delivered = true
	return nil
}
