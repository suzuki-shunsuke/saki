package herdr_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/herdr"
)

func TestParseAgents(t *testing.T) {
	t.Parallel()
	// The shape of "herdr agent list" of herdr 0.9.5, with fields this package doesn't use.
	out := []byte(`{"id":"cli:agent:list","result":{"agents":[
{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"24cc98bb-02b3-404f-8de0-1e7172270b06"},"agent_status":"idle","pane_id":"w5:p50"},
{"agent":"claude","agent_status":"idle","pane_id":"w5:p5G"},
{"agent":"saki","agent_session":null,"pane_id":"w5:p61"}
]}}`)
	got, err := herdr.ParseAgents(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []herdr.Agent{
		{PaneID: "w5:p50", Kind: "claude", SessionID: "24cc98bb-02b3-404f-8de0-1e7172270b06"},
		{PaneID: "w5:p5G", Kind: "claude"},
		{PaneID: "w5:p61", Kind: "saki"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseAgents() (-want +got):\n%s", diff)
	}
}

func TestParseAgentsInvalid(t *testing.T) {
	t.Parallel()
	if _, err := herdr.ParseAgents([]byte("not json")); err == nil {
		t.Error("ParseAgents() returned no error for invalid output")
	}
}

func TestParseTabs(t *testing.T) {
	t.Parallel()
	// The shape of "herdr tab list" of herdr 0.9.5, with fields this package doesn't use.
	out := []byte(`{"id":"cli:tab:list","result":{"tabs":[
{"agent_status":"working","focused":false,"label":"review suzuki-shunsuke/tfaction#4404","number":113,"pane_count":1,"tab_id":"w5:t3H","workspace_id":"w5"},
{"agent_status":"unknown","focused":true,"label":"1","number":1,"pane_count":1,"tab_id":"w6:t1","workspace_id":"w6"}
],"type":"tab_list"}}`)
	got, err := herdr.ParseTabs(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []herdr.Tab{
		{TabID: "w5:t3H", Label: "review suzuki-shunsuke/tfaction#4404", WorkspaceID: "w5"},
		{TabID: "w6:t1", Label: "1", WorkspaceID: "w6"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseTabs() (-want +got):\n%s", diff)
	}
}

func TestParseTabsInvalid(t *testing.T) {
	t.Parallel()
	if _, err := herdr.ParseTabs([]byte("not json")); err == nil {
		t.Error("ParseTabs() returned no error for invalid output")
	}
}

func TestIsNotRunning(t *testing.T) {
	t.Parallel()
	if !herdr.IsNotRunning(&herdr.Error{Code: herdr.CodeServerNotRunning, Message: "not running"}) {
		t.Error("IsNotRunning() = false for server_not_running")
	}
	if herdr.IsNotRunning(&herdr.Error{Code: "agent_not_found", Message: "not found"}) {
		t.Error("IsNotRunning() = true for agent_not_found")
	}
}
