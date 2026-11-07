module github.com/kamalyes/go-cachex

go 1.25.0

require (
	github.com/alicebob/miniredis/v2 v2.35.0
	github.com/dgraph-io/ristretto/v2 v2.4.0
	github.com/kamalyes/go-toolbox v0.19.3
	github.com/redis/go-redis/v9 v9.18.0
	github.com/stretchr/testify v1.12.1
	gorm.io/gorm v1.30.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

// 本地开发替换
// replace github.com/kamalyes/go-toolbox => ../go-toolbox

// replace github.com/kamalyes/go-logger => ../go-logger
