package tests

import (
	"context"
	"testing"

	"github.com/ggoodman/mcp-server-go/examples/echo"
	"github.com/ggoodman/mcp-server-go/examples/resources_per_session"
	"github.com/ggoodman/mcp-server-go/examples/resources_static"
	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/sessions"
	"github.com/ggoodman/mcp-server-go/sessions/memoryhost"
)

type fakeSession struct{ id, user string }

func (s fakeSession) SessionID() string       { return s.id }
func (s fakeSession) UserID() string          { return s.user }
func (s fakeSession) ProtocolVersion() string { return "" }
func (s fakeSession) ConsumeMessages(ctx context.Context, last string, f sessions.MessageHandlerFunction) error {
	return nil
}
func (s fakeSession) WriteMessage(ctx context.Context, msg []byte) error { return nil }
func (s fakeSession) GetSamplingCapability() (sessions.SamplingCapability, bool) {
	return nil, false
}
func (s fakeSession) GetRootsCapability() (sessions.RootsCapability, bool) { return nil, false }
func (s fakeSession) GetElicitationCapability() (sessions.ElicitationCapability, bool) {
	return nil, false
}

func TestLib_EchoTool(t *testing.T) {
	t.Parallel()
	srv := echo.New()
	sess := fakeSession{id: "s1", user: "u1"}
	ctx := context.Background()

	tools, ok, err := srv.GetToolsCapability(ctx, sess)
	if err != nil || !ok || tools == nil {
		t.Fatalf("tools capability missing: ok=%v err=%v", ok, err)
	}
	page, err := tools.ListTools(ctx, sess, nil)
	if err != nil || len(page.Items) != 1 || page.Items[0].Name != "echo" {
		t.Fatalf("unexpected tools list: %+v err=%v", page, err)
	}
	res, err := tools.CallTool(ctx, sess, &mcp.CallToolRequestReceived{Name: "echo", Arguments: []byte(`{"message":"hi"}`)})
	if err != nil || len(res.Content) == 0 {
		t.Fatalf("unexpected call result: %+v err=%v", res, err)
	}
}

func TestLib_ResourcesStatic(t *testing.T) {
	t.Parallel()
	srv := resources_static.New()
	sess := fakeSession{id: "s2", user: "u1"}
	ctx := context.Background()

	resCap, ok, err := srv.GetResourcesCapability(ctx, sess)
	if err != nil || !ok || resCap == nil {
		t.Fatalf("resources capability missing: ok=%v err=%v", ok, err)
	}
	page, err := resCap.ListResources(ctx, sess, nil)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("unexpected list: %+v err=%v", page, err)
	}
	_, err = resCap.ReadResource(ctx, sess, page.Items[0].URI)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
}

func TestLib_ResourcesPerSession(t *testing.T) {
	t.Parallel()
	srv := resources_per_session.New()
	ctx := context.Background()
	// Two sessions with different users
	a := fakeSession{id: "sa", user: "alice"}
	b := fakeSession{id: "sb", user: "bob"}

	resCapA, ok, err := srv.GetResourcesCapability(ctx, a)
	if err != nil || !ok || resCapA == nil {
		t.Fatalf("cap A missing: ok=%v err=%v", ok, err)
	}
	resCapB, ok, err := srv.GetResourcesCapability(ctx, b)
	if err != nil || !ok || resCapB == nil {
		t.Fatalf("cap B missing: ok=%v err=%v", ok, err)
	}

	pageA, _ := resCapA.ListResources(ctx, a, nil)
	pageB, _ := resCapB.ListResources(ctx, b, nil)
	if len(pageA.Items) != 1 || len(pageB.Items) != 1 || pageA.Items[0].URI == pageB.Items[0].URI {
		t.Fatalf("expected distinct per-user URIs: A=%+v B=%+v", pageA, pageB)
	}
}

// Ensure memoryhost remains import-referenced (avoid accidental removal as dead code).
var _ = memoryhost.New
