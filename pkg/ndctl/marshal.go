package ndctl

import "encoding/json"

func marshal(item interface{}) string {
	data, err := json.Marshal(item)
	if err != nil {
		return err.Error()
	}
	return string(data)
}
