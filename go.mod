module github.com/polarismesh/polaris-go

go 1.15

require (
	github.com/agiledragon/gomonkey v2.0.2+incompatible
	github.com/dlclark/regexp2 v1.7.0
	github.com/golang/protobuf v1.5.2
	github.com/gonum/blas v0.0.0-20181208220705-f22b278b28ac // indirect
	github.com/gonum/floats v0.0.0-20181209220543-c233463c7e82 // indirect
	github.com/gonum/integrate v0.0.0-20181209220457-a422b5c0fdf2 // indirect
	github.com/gonum/internal v0.0.0-20181124074243-f884aa714029 // indirect
	github.com/gonum/lapack v0.0.0-20181123203213-e4cdc5a0bff9 // indirect
	github.com/gonum/matrix v0.0.0-20181209220409-c518dec07be9 // indirect
	github.com/gonum/stat v0.0.0-20181125101827-41a0da705a5b
	github.com/google/uuid v1.3.0
	github.com/hashicorp/go-multierror v1.1.1
	github.com/mitchellh/go-homedir v1.1.0
	github.com/modern-go/reflect2 v1.0.2
	github.com/natefinch/lumberjack v2.0.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/polarismesh/specification v1.3.2-alpha.2
	github.com/prometheus/client_golang v1.12.2
	github.com/smartystreets/goconvey v1.7.2
	github.com/spaolacci/murmur3 v1.1.0
	github.com/stretchr/testify v1.8.2
	go.uber.org/zap v1.21.0
	golang.org/x/net v0.2.0
	google.golang.org/genproto v0.0.0-20221014213838-99cd37c6964a // indirect
	google.golang.org/grpc v1.51.0
	google.golang.org/protobuf v1.28.1
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	gopkg.in/yaml.v2 v2.4.0
)

replace (
	golang.org/x/net v0.2.0 => golang.org/x/net v0.0.0-20221019024206-cb67ada4b0ad
	golang.org/x/sys v0.2.0 => golang.org/x/sys v0.0.0-20220906165534-d0df966e6959
	google.golang.org/grpc v1.51.0 => google.golang.org/grpc v1.46.2
)
