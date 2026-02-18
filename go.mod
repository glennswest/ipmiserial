module console-server

go 1.24.2

require (
	github.com/gorilla/mux v1.8.1
	github.com/gwest/go-sol v0.0.0
	github.com/sirupsen/logrus v1.9.3
	gopkg.in/yaml.v3 v3.0.1
)

require golang.org/x/sys v0.13.0 // indirect

replace github.com/gwest/go-sol => github.com/glennswest/go-sol v0.0.0-20260205172125-7e054e8e59ab
