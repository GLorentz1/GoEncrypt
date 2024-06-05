build:
	tailwindcss -i public/css/styles.css -o public/styles.css
	@templ generate view
	@go build -o bin/goencrypt main.go


run: build
	@./bin/goencrypt

tailwind:
	@tailwindcss -i views/css/styles.css -o public/styles.css --watch

templ:
	@templ generate -watch -proxy=http://localhost:56789
