// Copyright Quesma, licensed under the Elastic License 2.0.
// SPDX-License-Identifier: Elastic-2.0
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var GrafanaLibraryExtractDetails = mcpgrafana.MustTool(
	"style_transfer_extract",
	"Extract information from a dashboard. Let's call it a template dashboard. Extracted information is a JSON object with the dashboard title and panels.  Dashboards comes from the Grafana library  https://grafana.com/grafana/dashboards/. ",
	grafanaLibraryExtractDetails,
	mcp.WithTitleAnnotation("Extract Dashboard Details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type GrafanaLibraryExtractDetailsParams struct {
	URL string `json:"url" jsonschema:"description=URL of the dashboard to extract information from.  Dashboards comes from the Grafana library  https://grafana.com/grafana/dashboards/."`
}

func grafanaLibraryExtractDetails(ctx context.Context, args GrafanaLibraryExtractDetailsParams) (string, error) {

	dashboardId := args.URL

	dashboard, err := findDashboard(dashboardId)
	if err != nil {
		return "", fmt.Errorf("failed to find dashboard with ID %s: %w", dashboardId, err)
	}

	dashboardInfo := dashboard.extractDashboardInfo()

	content, err := dashboardInfo.serialize()
	if err != nil {
		return "", fmt.Errorf("failed to serialize dashboard info: %w", err)
	}

	return string(content), nil
}

var GrafanaLibraryApplyDetails = mcpgrafana.MustTool(
	"style_transfer_apply",
	"Create a new instance of a dashboard basing on the information extracted from the template dashboard and the information provided. The dashboard_info is the output of the extract_dashboard_info tool. It is a JSON object with the dashboard title and panels. It returns the URL of the new dashboard.",
	grafanaLibraryApplyDetails,
	mcp.WithTitleAnnotation("Apply details  and create a new dashboard"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type GrafanaLibraryApplyDetailsParams struct {
	URL    string `json:"url" jsonschema:"description=URL of the dashboard that information was extracted from. This is the same URL used in extract function Grafana library  https://grafana.com/grafana/dashboards/."`
	Detail string `json:"detail" jsonschema:"description=Information about the dashboard to create an instance of. This is the output of the transfer_style_tool tool. It is a JSON object with the dashboard title and panels."`
}

func grafanaLibraryApplyDetails(ctx context.Context, args GrafanaLibraryApplyDetailsParams) (string, error) {

	dashboardId := args.URL
	if dashboardId == "" {
		return "", fmt.Errorf("dashboard ID is required")
	}

	dashboardInfoStr := args.Detail

	if dashboardInfoStr == "" {
		return "", fmt.Errorf("dashboard info is required")
	}

	dashboard, err := findDashboard(dashboardId)
	if err != nil {
		return "", fmt.Errorf("failed to find dashboard with ID %s: %w", dashboardId, err)
	}

	if dashboard == nil {
		return "", fmt.Errorf("dashboard with ID %s not found", dashboardId)
	}

	dashboardInfo, err := praseDashboardInfo(dashboardInfoStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse dashboard info: %w", err)
	}

	dashboard.applyDashboardInfo(dashboardInfo)

	content, err := dashboard.serialize()
	if err != nil {
		return "", fmt.Errorf("failed to serialize dashboard: %w", err)
	}

	dashboardUrl, err := createDashboard(string(content))
	if err != nil {
		return "", fmt.Errorf("failed to create dashboard: %w", err)
	}

	return dashboardUrl, nil
}

type Dashboard struct {
	Title       string      `json:"title"`
	UID         string      `json:"uid"`
	Version     int         `json:"version"`
	Editable    bool        `json:"editable"`
	Templating  Templating  `json:"templating"`
	Panels      []Panel     `json:"panels"`
	Annotations Annotations `json:"annotations"`
	Time        Time        `json:"time"`
	Inputs      []Input     `json:"__inputs"`
	Requires    []Require   `json:"__requires"`
}

type Time struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Templating struct {
	List []TemplateVar `json:"list"`
}

type TemplateVar struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Query   any      `json:"query"`
	Current Current  `json:"current"`
	Options []Option `json:"options"`
}

type Current struct {
	Text  interface{} `json:"text"`
	Value interface{} `json:"value"`
}

type Option struct {
	Text  string      `json:"text"`
	Value interface{} `json:"value"`
}

type Annotations struct {
	List []Annotation `json:"list"`
}

type Annotation struct {
	Name       string `json:"name"`
	Datasource any    `json:"datasource"`
	Enable     bool   `json:"enable"`
	Type       any    `json:"type"`
}

type Input struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Type        string `json:"type"`
	PluginID    string `json:"pluginId"`
	PluginName  string `json:"pluginName"`
}

type Require struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Target struct {
	Expr           string `json:"expr"`
	Format         string `json:"format"`                 // e.g. "time_series"
	Interval       string `json:"interval"`               // e.g. "10s"
	IntervalFactor int    `json:"intervalFactor"`         // e.g. 1
	RefID          string `json:"refId"`                  // e.g. "A"
	Step           int    `json:"step"`                   // e.g. 10
	LegendFormat   string `json:"legendFormat,omitempty"` // optional
	Metric         string `json:"metric,omitempty"`       // optional
	Datasource     any    `json:"datasource,omitempty"`   // may be string or object
}

type Panel struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Targets     []Target `json:"targets"`
	Description string   `json:"description"`
	Panels      []Panel  `json:"panels,omitempty"` // for "row" type

	// the rest can be type of any

	Datasource      interface{}   `json:"datasource"`
	GridPos         GridPos       `json:"gridPos"`
	AliasColors     any           `json:"aliasColors,omitempty"`
	Legend          *Legend       `json:"legend,omitempty"`
	XAxis           *Axis         `json:"xaxis,omitempty"`
	YAxis           *YAxisOptions `json:"yaxis,omitempty"`
	YAxes           []AxisOptions `json:"yaxes,omitempty"`
	Thresholds      interface{}   `json:"thresholds,omitempty"`
	Transform       string        `json:"transform,omitempty"`
	Columns         []TableColumn `json:"columns,omitempty"`
	Styles          []TableStyle  `json:"styles,omitempty"`
	Tooltip         any           `json:"tooltip,omitempty"`
	Collapsed       bool          `json:"collapsed,omitempty"`
	Repeat          string        `json:"repeat,omitempty"`
	ColorBackground bool          `json:"colorBackground,omitempty"`
	Colors          any           `json:"colors,omitempty"`
	Gauge           any           `json:"gauge,omitempty"`
	Links           any           `json:"links,omitempty"`
	FieldsConfig    any           `json:"fieldsConfig,omitempty"`
	Options         any           `json:"options,omitempty"`
}

type GridPos struct {
	H any `json:"h"`
	W any `json:"w"`
	X any `json:"x"`
	Y any `json:"y"`
}

type Legend struct {
	Show         bool `json:"show"`
	Avg          bool `json:"avg"`
	Current      bool `json:"current"`
	Min          bool `json:"min"`
	Max          bool `json:"max"`
	Total        bool `json:"total"`
	Values       bool `json:"values"`
	AlignAsTable bool `json:"alignAsTable"`
	SideWidth    int  `json:"sideWidth,omitempty"`
}

type Axis struct {
	Show   bool   `json:"show"`
	Mode   string `json:"mode"`
	Format string `json:"format,omitempty"`
	Name   string `json:"name,omitempty"`
}

type AxisOptions struct {
	Format  string `json:"format"`
	Label   string `json:"label"`
	Show    bool   `json:"show"`
	LogBase int    `json:"logBase"`
	Min     any    `json:"min,omitempty"`
	Max     any    `json:"max,omitempty"`
}

type YAxisOptions struct {
	Align      bool        `json:"align"`
	AlignLevel interface{} `json:"alignLevel"`
}

type TableColumn struct {
	Text  string `json:"text"`
	Value string `json:"value"`
}

type TableStyle struct {
	Pattern    string   `json:"pattern"`
	Type       string   `json:"type"`
	ColorMode  string   `json:"colorMode,omitempty"`
	Colors     []string `json:"colors,omitempty"`
	Decimals   int      `json:"decimals,omitempty"`
	DateFormat string   `json:"dateFormat,omitempty"`
	Thresholds []string `json:"thresholds,omitempty"`
	Unit       string   `json:"unit,omitempty"`
}

func parseDashboard(content string) (*Dashboard, error) {

	var dashboard Dashboard
	if err := json.Unmarshal([]byte(content), &dashboard); err != nil {
		return nil, err
	}

	return &dashboard, nil
}

type DashboardRepoResponse struct {
	ID          int            `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Version     int            `json:"version"`
	Dashboard   map[string]any `json:"json"` // raw JSON of the dashboard
}

func extractIDFromURL(raw string) (int, error) {
	re := regexp.MustCompile(`/dashboards/(\d+)`)
	matches := re.FindStringSubmatch(strings.TrimSpace(raw))
	if len(matches) > 1 {
		id, err := strconv.Atoi(matches[1])
		if err == nil {
			return id, nil
		}
	}
	return 0, fmt.Errorf("could not locate numeric dashboard ID in URL %s", raw)

}

func downloadDashboardFromGrafanaLibrary(url string) (string, error) {

	if !strings.HasPrefix(url, "https://grafana.com/grafana/dashboards/") {
		return "", fmt.Errorf("invalid Grafana dashboard URL: %s. Expected format: https://grafana.com/grafana/dashboards/<id>-something", url)
	}

	id, err := extractIDFromURL(url)
	if err != nil {
		return "", fmt.Errorf("failed to extract ID from URL %s: %w", url, err)
	}

	log.Printf("Downloading dashboard with ID %d", id)

	apiURL := fmt.Sprintf("https://grafana.com/api/dashboards/%d", id)

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download dashboard: %s", resp.Status)
	}

	var data DashboardRepoResponse
	if err = json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("failed to decode JSON: %w", err)
	}

	if data.Dashboard == nil {
		return "", fmt.Errorf("dashboard JSON is empty or not found in response")
	}

	pretty, err := json.MarshalIndent(data.Dashboard, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal dashboard JSON: %w", err)
	}

	return string(pretty), nil

}

func findDashboard(url string) (*Dashboard, error) {

	// right now we only support URLs from the Grafana library
	content, err := downloadDashboardFromGrafanaLibrary(url)
	if err != nil {
		log.Printf("Failed to download dashboard from Grafana library: %s\n", err)
		return nil, fmt.Errorf("failed to download dashboard from Grafana library: %w", err)
	}

	dashboard, err := parseDashboard(content)
	if err != nil {
		return nil, err
	}
	return dashboard, nil
}

func (d *Dashboard) serialize() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

func (d *Dashboard) printStats() {
	fmt.Printf("📊 Dashboard: %s (Panels: %d)\n", d.Title, len(d.Panels))
	for _, p := range d.Panels {
		fmt.Printf("  • Panel %d: %s [%s] \n", p.ID, p.Title, p.Type)
	}
}

type DashboardInfo struct {
	Title  string      `json:"title,omitempty"`
	Panels []PanelInfo `json:"panels"`
}

type PanelInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`

	Targets []TargetInfo `json:"targets,omitempty"`
}

type TargetInfo struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

func praseDashboardInfo(response string) (*DashboardInfo, error) {

	var dashboardInfo DashboardInfo
	err := json.Unmarshal([]byte(response), &dashboardInfo)
	if err != nil {
		log.Printf("Error unmarshalling dashboard info: %s\n", err)
		return nil, err
	}
	return &dashboardInfo, nil
}

func (d *DashboardInfo) serialize() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

func (d *DashboardInfo) save(filename string) error {
	content, err := d.serialize()
	if err != nil {
		return err
	}
	return os.WriteFile(filename, content, 0644)
}

func (d *DashboardInfo) printDashboardInfo() {

	content, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		fmt.Printf("Error marshalling dashboard info: %s\n", err)
		return
	}
	fmt.Println(string(content))
}

func extractPanel(panel *Panel, dashboardInfo *DashboardInfo, maxPanels int) {

	id := fmt.Sprintf("%d", panel.ID)
	log.Printf("Extracting dashboard info for panel: %s\n", id)
	panelInfo := PanelInfo{
		ID:    id,
		Title: panel.Title,
		Type:  panel.Type,
	}

	targets := make([]TargetInfo, len(panel.Targets))

	for i, t := range panel.Targets {
		log.Printf("Extracting dashboard info for target: %s\n", t.RefID)
		targets[i] = TargetInfo{
			ID:    t.RefID,
			Query: t.Expr,
		}
	}

	panelInfo.Targets = targets
	dashboardInfo.Panels = append(dashboardInfo.Panels, panelInfo)

	for _, p := range panel.Panels {
		maxPanels--
		if maxPanels <= 0 {
			break
		}
		extractPanel(&p, dashboardInfo, maxPanels)
	}

}

func (d *Dashboard) extractDashboardInfo() *DashboardInfo {

	// extract only 20 panels, to avoid overwhelming the LLM
	maxPanels := 20

	dashboardInfo := &DashboardInfo{
		Title:  d.Title,
		Panels: make([]PanelInfo, 0),
	}

	for _, p := range d.Panels {
		maxPanels--
		if maxPanels <= 0 {
			break
		}
		extractPanel(&p, dashboardInfo, maxPanels)
	}
	return dashboardInfo
}

func updatePanel(panel *Panel, dashboardInfo *DashboardInfo) {

	id := fmt.Sprintf("%d", panel.ID)

	var panelInfo *PanelInfo
	for _, p := range dashboardInfo.Panels {
		if p.ID == id {
			panelInfo = &p
			break
		}
	}

	if panelInfo != nil {

		panel.Title = panelInfo.Title
		panel.Type = panelInfo.Type

		for i := range panel.Targets {
			var targetInfo *TargetInfo

			for _, t := range panelInfo.Targets {
				if t.ID == panel.Targets[i].RefID {
					targetInfo = &t
					break
				}
			}

			if targetInfo != nil {
				panel.Targets[i].Expr = targetInfo.Query
			} else {
				log.Printf("Could not find target info for panel ID: %s\n", id)
			}
		}

	} else {
		log.Printf("Could not find panel ID: %s\n", id)
	}

	for _, p := range panel.Panels {
		updatePanel(&p, dashboardInfo)
	}

}

func (d *Dashboard) applyDashboardInfo(dashboardInfo *DashboardInfo) {

	d.Title = dashboardInfo.Title

	for i := range d.Panels {
		// Use the actual panel ID, not the slice index

		panel := &d.Panels[i]
		updatePanel(panel, dashboardInfo)

	}
}

func createDashboard(dashboardJSON string) (string, error) {

	grafanaBaseURL := os.Getenv("GRAFANA_URL")
	grafanaApiKey := os.Getenv("GRAFANA_API_KEY")

	if grafanaBaseURL == "" {
		return "", fmt.Errorf("GRAFANA_URL environment variable is not set")
	}

	if grafanaApiKey == "" {
		return "", fmt.Errorf("GRAFANA_API_KEY environment variable is not set")
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(dashboardJSON), &raw); err != nil {
		log.Fatalf("Invalid dashboard JSON: %v", err)
	}

	dashboard, ok := raw["dashboard"].(map[string]interface{})
	if !ok {
		dashboard = raw // fallback in case it's already unwrapped
	}

	// Ensure "id" is nil (for fresh import)
	dashboard["id"] = nil
	dashboard["uid"] = nil

	// Ensure "uid" is nil
	title := dashboard["title"].(string)
	dashboard["title"] = title + " (Imported) at " + time.Now().Format("2006-01-02 15:04:05")

	// Check for __inputs and build inputs array
	var inputs []map[string]interface{}
	if rawInputs, ok := dashboard["__inputs"].([]interface{}); ok {
		for _, in := range rawInputs {
			if m, ok := in.(map[string]interface{}); ok {
				name := m["name"].(string)
				pluginId := m["pluginId"].(string)
				inputs = append(inputs, map[string]interface{}{
					"name":     name,
					"type":     "datasource",
					"pluginId": pluginId,
					"value":    "Prometheus",
				})
			}
		}
	}

	// Final import payload
	wrapped := map[string]interface{}{
		"dashboard": dashboard,
		"overwrite": false,
		"folderId":  0,
	}
	if len(inputs) > 0 {
		log.Println("Inputs:")
		log.Println(inputs)
		wrapped["inputs"] = inputs
	}

	payload, err := json.MarshalIndent(wrapped, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal dashboard payload: %w", err)
	}

	client := http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", grafanaBaseURL+"/api/dashboards/import", bytes.NewBuffer(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+grafanaApiKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	log.Println("Response:")
	log.Println(string(body))

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to import dashboard: %s", resp.Status)
	}

	var result struct {
		Slug string `json:"slug"`
		URL  string `json:"importedUrl"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %v", err)
	}

	return grafanaBaseURL + result.URL, nil
}

func AddStyleTransfer(mcp *server.MCPServer) {
	log.Println("Adding style transfer tools")
	GrafanaLibraryExtractDetails.Register(mcp)
	GrafanaLibraryApplyDetails.Register(mcp)
}
