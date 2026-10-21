// Package adminconfig handles non-secret administrative defaults.
// The operator DSN and credentials are intentionally environment-only.
package adminconfig

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "os"
)

const maxFileSize=64<<10

type Config struct {
    Version int `json:"version"`
    RetentionDays int `json:"retention_days"`
    RetentionBatch int `json:"retention_batch"`
    RetentionMaxDelete int `json:"retention_max_delete"`
    BackfillBatch int `json:"backfill_batch"`
}

func Defaults() Config {
    return Config{Version:1,RetentionDays:0,RetentionBatch:500,
        RetentionMaxDelete:10000,BackfillBatch:100}
}

// Load requires an explicitly selected path. Unknown keys, duplicate JSON
// documents and secret-shaped configuration keys are rejected.
func Load(path string)(Config,error){
    cfg:=Defaults()
    if path==""{return cfg,nil}
    f,err:=os.Open(path)
    if err!=nil{return Config{},fmt.Errorf("open admin config: %w",err)}
    defer f.Close()
    raw,err:=io.ReadAll(io.LimitReader(f,maxFileSize+1))
    if err!=nil{return Config{},fmt.Errorf("read admin config: %w",err)}
    if len(raw)>maxFileSize{return Config{},fmt.Errorf("admin config exceeds 64 KiB")}
    dec:=json.NewDecoder(bytes.NewReader(raw))
    dec.DisallowUnknownFields()
    // Zero values in JSON are not silently turned into safe defaults:
    // decode from a defaults struct only for genuinely omitted keys.
    if err:=dec.Decode(&cfg);err!=nil{return Config{},fmt.Errorf("invalid admin config: %w",err)}
    var extra any
    if err:=dec.Decode(&extra);err!=io.EOF{return Config{},fmt.Errorf("admin config must contain one JSON object")}
    if err:=cfg.Validate();err!=nil{return Config{},err}
    return cfg,nil
}

func(c Config)Validate()error{
    if c.Version!=1 {return fmt.Errorf("admin config version must be 1")}
    if c.RetentionDays!=0 && (c.RetentionDays<7||c.RetentionDays>3650) {
        return fmt.Errorf("retention_days must be 7..3650 or omitted")
    }
    if c.RetentionBatch<1||c.RetentionBatch>2000 {
        return fmt.Errorf("retention_batch must be 1..2000")
    }
    if c.RetentionMaxDelete<1||c.RetentionMaxDelete>1000000{
        return fmt.Errorf("retention_max_delete must be 1..1000000")
    }
    if c.BackfillBatch<1||c.BackfillBatch>500{
        return fmt.Errorf("backfill_batch must be 1..500")
    }
    return nil
}
