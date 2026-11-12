package images

import "testing"

func TestDefaultImagesJSONPreservesWireBody(t *testing.T) {
	const body = `{ "model":"native", "prompt":"<hello>", "future":{"id":9007199254740993} }`
	got, _, err := buildJSONRequestBody([]byte(body), "native", "native", operationGenerations)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("default body changed: %s", got)
	}
}
