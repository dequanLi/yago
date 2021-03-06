package excellib

import (
	"os"
	"testing"
)

func TestAll(t *testing.T) {
	t.Run("export-assoc", testExportAssoc)
	t.Run("import", testImport)
	t.Run("export", testExportRows)
	t.Run("import", testImport)
	// t.Cleanup
	err := os.Remove("user.xlsx")
	if err != nil {
		t.Error(err)
	}
}

func testExportAssoc(t *testing.T) {
	rows := []map[string]interface{}{
		{"name": "张三", "age": 1000000000003},
		{"name": "李四", "age": 20},
	}

	excelFile, err := ExportAssoc(&ExportAssocReq{
		Rows:      rows,
		Headers:   D{{Key: "name", Value: "姓名"}, {Key: "age", Value: "年龄"}},
		AutoAlign: true,
	})

	if err != nil {
		t.Errorf("export assoc err:%s", err.Error())
		return
	}

	err = excelFile.SaveAs("user.xlsx")
	if err != nil {
		t.Errorf("save err:%s", err.Error())
	}

}

func testExportRows(t *testing.T) {
	rows := [][]interface{}{
		{"王五", 20232},
		{"刘六", 30},
		{"田七", 19},
	}

	excelFile, err := Export(&ExportReq{
		Rows:      rows,
		Headers:   []string{"姓名", "年龄"},
		AutoAlign: true,
	})

	if err != nil {
		t.Errorf("export err:%s", err.Error())
		return
	}

	err = excelFile.SaveAs("user.xlsx")
	if err != nil {
		t.Errorf("save err:%s", err.Error())
	}
}

func testImport(t *testing.T) {

	resp, err := ImportFile(&ImportReq{
		Filename:       "user.xlsx",
		DivideFirstRow: true,
	})

	if err != nil {
		t.Errorf("import err:%s", err.Error())
		return
	}

	t.Logf("headers: %+v\n", resp.Headers)

	t.Logf("rows:%+v\n", resp.Rows)
}
