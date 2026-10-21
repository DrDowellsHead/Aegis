package history

import (
    "encoding/base64"
    "encoding/json"
    "net/http"
    "time"

    "github.com/DrDowellsHead/Aegis/internal/storage"
)

// ParseHistoryRequest and ParseSeriesRequest share the exact same validated
// query contract with the loopback history API. Dashboard access controls
// remain separate; callers MUST verify browser authentication first.
func ParseHistoryRequest(r *http.Request)(storage.HistoryQuery,error){return parseQuery(r)}
func ParseSeriesRequest(r *http.Request)(storage.SeriesQuery,error){return parseSeriesQuery(r)}

type Page struct {
    Items []storage.HistoryItem `json:"items"`
    HasMore bool `json:"has_more"`
    NextCursor string `json:"next_cursor,omitempty"`
}

// BuildPage uses the existing history API cursor shape, binding cursor to
// agent and time filters. No keyset cursor is ever used as SQL syntax.
func BuildPage(q storage.HistoryQuery,result storage.HistoryResult)(Page,error){
    p:=Page{Items:result.Items,HasMore:result.HasMore}
    if !result.HasMore||len(result.Items)==0{return p,nil}
    last:=result.Items[len(result.Items)-1]
    from,to:="",""
    if !q.From.IsZero(){from=q.From.UTC().Format(time.RFC3339Nano)}
    if !q.To.IsZero(){to=q.To.UTC().Format(time.RFC3339Nano)}
    raw,err:=json.Marshal(cursorData{
        AgentID:q.AgentID,StartedAt:last.StartedAt,MeasurementID:last.MeasurementID,
        From:from,To:to,
    })
    if err!=nil{return Page{},err}
    p.NextCursor=base64.RawURLEncoding.EncodeToString(raw)
    return p,nil
}
