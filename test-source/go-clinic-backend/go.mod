module clinic

go 1.25.0

require sonic-screwdriver/sdk/go v0.0.0

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
)

// The SDK from this repo; in a real app this would be a normal module version.
replace sonic-screwdriver/sdk/go => ../../sdk/go
