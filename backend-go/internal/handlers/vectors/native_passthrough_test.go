package vectors

import "testing"

func TestDefaultVectorsJSONPreservesWireBody(t *testing.T) {
	const body = `{ "model":"native", "input":[1,2,3], "stream":false, "future":{"id":9007199254740993} }`
	got, err := buildEmbeddingsRequestBody([]byte(body), "native", "native")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("default body changed: %s", got)
	}
}
