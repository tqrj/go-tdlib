package codegen

import (
	"bytes"
	"fmt"
	"github.com/tqrj/go-tdlib/internal/tlparser"
)

func GenerateUnmarshalers(schema *tlparser.Schema, packageName string) []byte {
	buf := bytes.NewBufferString("")

	buf.WriteString(fmt.Sprintf("%s\npackage %s\n\n", header, packageName))

	buf.WriteString(`import (
    "encoding/json/v2"
    "encoding/json/jsontext"
    "fmt"
)

`)

	for _, typ := range schema.Types {
		tdlibtype := TdlibType(typ.Name, schema)

		buf.WriteString(fmt.Sprintf(`func Unmarshal%s(data jsontext.Value) (%s, error) {
    var meta meta
    err := json.Unmarshal(data, &meta)
    if err != nil {
        return nil, err
    }

    switch meta.MetaType {
`, tdlibtype.ToGoType(), tdlibtype.ToGoType()))

		for _, constructor := range tdlibtype.GetConstructors() {
			buf.WriteString(fmt.Sprintf(`    case %s:
        return Unmarshal%s(data)

`, constructor.ToConstructorConst(), constructor.ToGoType()))

		}

		buf.WriteString(`    default:
        return nil, fmt.Errorf("Error unmarshaling. Unknown type: %s", meta.MetaType)
    }
}

`)

		buf.WriteString(fmt.Sprintf(`func UnmarshalListOf%s(dataList []jsontext.Value) ([]%s, error) {
    list := make([]%s, 0, len(dataList))
    for _, data := range dataList {
        entity, err := Unmarshal%s(data)
        if err != nil {
            return nil, err
        }
        list = append(list, entity)
    }

    return list, nil
}

`, tdlibtype.ToGoType(), tdlibtype.ToGoType(), tdlibtype.ToGoType(), tdlibtype.ToGoType()))

	}

	for _, constructor := range schema.Constructors {
		tdlibConstructor := TdlibConstructor(constructor.Name, schema)

		if tdlibConstructor.IsList() || tdlibConstructor.IsInternal() {
			continue
		}

		buf.WriteString(fmt.Sprintf(`func Unmarshal%s(data jsontext.Value) (*%s, error) {
    var resp %s
    err := json.Unmarshal(data, &resp)
    return &resp, err
}

`, tdlibConstructor.ToGoType(), tdlibConstructor.ToGoType(), tdlibConstructor.ToGoType()))

	}

	buf.WriteString(`func UnmarshalType(data jsontext.Value) (Type, error) {
    var meta meta
    err := json.Unmarshal(data, &meta)
    if err != nil {
        return nil, err
    }

    switch meta.MetaType {
`)

	for _, constructor := range schema.Constructors {
		tdlibConstructor := TdlibConstructor(constructor.Name, schema)

		if tdlibConstructor.IsList() || tdlibConstructor.IsInternal() {
			continue
		}

		buf.WriteString(fmt.Sprintf(`    case %s:
        return Unmarshal%s(data)

`, tdlibConstructor.ToConstructorConst(), tdlibConstructor.ToGoType()))

	}

	buf.WriteString(`    default:
        return nil, fmt.Errorf("Error unmarshaling. Unknown type: %s", meta.MetaType)
    }
}
`)

	return buf.Bytes()
}
