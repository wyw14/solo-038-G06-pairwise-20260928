package splice

import (
	"testing"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

func TestSeamRouterPropagatesInOrder(t *testing.T) {
	router, err := NewSeamRouter(ProductionRoute)
	if err != nil {
		t.Fatalf("router construction failed: %v", err)
	}
	certificate, err := Inspect(measurement(50, 200), time.Now())
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if err := router.Enter(certificate); err != nil {
		t.Fatalf("enter failed: %v", err)
	}
	if router.ArrivedAtFolder(certificate.ID) {
		t.Fatal("a fresh conclusion must not be visible at the folder")
	}
	if err := router.Pass(certificate.ID, "folder", 155); err == nil {
		t.Fatal("skipping press and dryer must be rejected")
	}
	for _, segment := range ProductionRoute {
		if err := router.Pass(certificate.ID, segment, certificate.Meter); err != nil {
			t.Fatalf("pass %s failed: %v", segment, err)
		}
	}
	if !router.ArrivedAtFolder(certificate.ID) {
		t.Fatal("conclusion must arrive at the folder after passing every segment")
	}
	if err := router.MarkDelivered(certificate.ID); err != nil {
		t.Fatalf("mark delivered failed: %v", err)
	}
	route, ok := router.Route(certificate.ID)
	if !ok || !route.Delivered {
		t.Fatalf("route trace missing delivery: %+v", route)
	}
	if len(route.Checkpoints) != len(ProductionRoute)+1 || route.Checkpoints[0].Segment != "splice" {
		t.Fatalf("checkpoints must start at the splice table and cover every segment: %+v", route.Checkpoints)
	}
	if err := router.Pass(certificate.ID, "press", 1); err == nil {
		t.Fatal("passing beyond the folder must be rejected")
	}
}

func TestSeamRouterRejectsUnknownCertificate(t *testing.T) {
	router, err := NewSeamRouter(ProductionRoute)
	if err != nil {
		t.Fatalf("router construction failed: %v", err)
	}
	if err := router.Pass(model.NewEventID(), "press", 1); err == nil {
		t.Fatal("unknown certificate must be rejected")
	}
	if _, err := NewSeamRouter(nil); err == nil {
		t.Fatal("empty route must be rejected")
	}
}
