module github.com/guilhem/freeipa-issuer

go 1.13

require (
	github.com/butonic/zerologr v0.0.0-20191210074216-d798ee237d84 // indirect
	github.com/go-logr/logr v0.3.0
	github.com/google/go-cmp v0.5.2
	github.com/google/martian v2.1.0+incompatible
	github.com/jetstack/cert-manager v0.16.1
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/smartystreets/assertions v1.0.0 // indirect
	github.com/stretchr/testify v1.6.1 // indirect
	github.com/tehwalris/go-freeipa v0.0.0-20200322083409-e462fc554b76
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/client-go v0.19.2
	k8s.io/klog/v2 v2.3.0 // indirect
	k8s.io/kube-aggregator v0.19.0 // indirect
	k8s.io/kubectl v0.19.0 // indirect
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800
	sigs.k8s.io/controller-runtime v0.7.0
	software.sslmate.com/src/go-pkcs12 v0.0.0-20200619203921-c9ed90bd32dc // indirect
)

replace github.com/tehwalris/go-freeipa => github.com/ccin2p3/go-freeipa v0.0.0-20201002173459-491486349e39
