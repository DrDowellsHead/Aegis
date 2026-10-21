package adminconfig

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestDefaultsAndStrictJSON(t *testing.T){
    got,err:=Load("")
    if err!=nil||got.RetentionDays!=0||got.RetentionBatch!=500{t.Fatal(got,err)}
    cases:=[]struct{json string;ok bool}{
        {`{"version":1,"retention_days":30,"retention_batch":50}`,true},
        {`{"version":2}`,false},
        {`{"retention_days":1}`,false},
        {`{"database_url":"postgres://secret"}`,false},
        {`{"token":"do-not-store-secrets"}`,false},
        {`{"backfill_batch":0}`,false},
        {`{"retention_max_delete":1000001}`,false},
        {`{} {}`,false},
        {`[]`,false},
    }
    for _,tc:=range cases{
        path:=filepath.Join(t.TempDir(),"admin.json")
        if err:=os.WriteFile(path,[]byte(tc.json),0600);err!=nil{t.Fatal(err)}
        _,err:=Load(path)
        if (err==nil)!=tc.ok{t.Errorf("config %s accepted=%v want=%v",tc.json,err==nil,tc.ok)}
    }
    path:=filepath.Join(t.TempDir(),"huge.json")
    if err:=os.WriteFile(path,[]byte(strings.Repeat("x",65537)),0600);err!=nil{t.Fatal(err)}
    if _,err:=Load(path);err==nil{t.Fatal("oversized config accepted")}
}
