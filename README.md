# go-project-316: "Sites parser"

### Hexlet tests and linter status:
[![Actions Status](https://github.com/alexvitayu/go-project-316/actions/workflows/hexlet-check.yml/badge.svg)](https://github.com/alexvitayu/go-project-316/actions)

### Local tests status:
[![tests-check](https://github.com/alexvitayu/go-project-316/actions/workflows/tests-check.yml/badge.svg)](https://github.com/alexvitayu/go-project-316/actions/workflows/tests-check.yml)

### Local linter status:
[![golangci-lint](https://github.com/alexvitayu/go-project-316/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/alexvitayu/go-project-316/actions/workflows/golangci-lint.yml)

The goal of this CLI utility is to crawl through the sites at the\
certain depth and collect the necessary information about the pages\
and the program returns the report which looks like shown below:
### An example of report:
```json
{
  "root_url": "https://example.com",
  "depth": 1,
  "generated_at": "2024-06-01T12:34:56Z",
  "pages": [
    {
      "url": "https://example.com",
      "depth": 0,
      "http_status": 200,
      "status": "ok",
      "error": "",
      "seo": {
        "has_title": true,
        "title": "Example title",
        "has_description": true,
        "description": "Example description",
        "has_h1": true
      },
      "broken_links": [
        {
          "url": "https://example.com/missing",
          "status_code": 404,
          "error": "Not Found"
        }
      ],
      "assets": [
        {
          "url": "https://example.com/static/logo.png",
          "type": "image",
          "status_code": 200,
          "size_bytes": 12345,
          "error": ""
        }
      ],
      "discovered_at": "2024-06-01T12:34:56Z"
    }
  ]
}
```
### An example of program enquiry:

````text
bin/hexlet-go-crawler --help
NAME:
   hexlet-go-crawler - analyze a website structure

USAGE:
   hexlet-go-crawler [global options] command [command options] <url>

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --depth value       crawl depth (default: 10)
   --retries value     number of retries for failed requests (default: 1)
   --delay value       delay between requests (example: 200ms, 1s) (default: 0s)
   --timeout value     per-request timeout (default: 15s)
   --rps value         limit requests per second (overrides delay) (default: 0)
   --user-agent value  custom user agent
   --workers value     number of concurrent workers (default: 4)
   --help, -h          show help
````

## Build and setup
```ch
git clone https://github.com/alexvitayu/go-project-316.git
```
```ch
cd go-project-316
``` 
```ch
make build  
```
## Run linter
```ch
make lint
```
## Run tests
```ch
make test
```
## Install
```ch
make install
```
## Help
```ch
hexlet-go-crawler -h --help
```
## An example of usage:
```ch
hexlet-go-crawler --user-agent=curl/8.14.1 --depth=2 --workers=8 --indent-json=true --timeout=30s https://wooordhunt.ru/
```