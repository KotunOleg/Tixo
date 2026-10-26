package api

import "net/http"

const swaggerPage = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>Tixo API docs</title>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.17.14/swagger-ui.min.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.17.14/swagger-ui-bundle.min.js"></script>
<script>
window.onload = () => SwaggerUIBundle({url: "/api/openapi.yaml", dom_id: "#swagger-ui"})
</script>
</body>
</html>`

func ServeDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerPage))
}

func ServeOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(openAPISpec)
}
