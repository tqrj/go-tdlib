package client

import "testing"

// 通过实际链接的原生库核对配套关系，不只检查生成文件中的版本常量。
// 此调用不创建账号或访问网络。
func TestLinkedTDLibMatchesSchema(t *testing.T) {
	for name, want := range map[string]string{
		"version":     "1.8.70",
		"commit_hash": TDLIB_VERSION,
	} {
		value, err := GetOption(&GetOptionRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := value.(*OptionValueString)
		if !ok || got.Value != want {
			t.Fatalf("linked TDLib %s = %v, want %q; rebuild TDLib and regenerate bindings together", name, value, want)
		}
	}
}
