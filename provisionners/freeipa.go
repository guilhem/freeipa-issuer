package provisioners

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
	certmanager "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	"github.com/jetstack/cert-manager/pkg/util/pki"
	"github.com/tehwalris/go-freeipa/freeipa"
	"k8s.io/apimachinery/pkg/types"
)

var collection = new(sync.Map)

// FreeIPAPKI
type FreeIPAPKI struct {
	client *freeipa.Client
	spec   *api.IssuerSpec

	name string
}

// New returns a new provisioner, configured with the information in the
// given issuer.
func New(namespacedName types.NamespacedName, spec *api.IssuerSpec, user, password string, insecure bool) (*FreeIPAPKI, error) {
	tspt := http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure,
		},
	}

	client, err := freeipa.Connect(spec.Host, &tspt, user, password)
	if err != nil {
		return nil, err
	}

	p := &FreeIPAPKI{
		name:   fmt.Sprintf("%s.%s", namespacedName.Name, namespacedName.Namespace),
		client: client,
		spec:   spec,
	}

	return p, nil
}

// Load returns a provisioner by NamespacedName.
func Load(namespacedName types.NamespacedName) (*FreeIPAPKI, bool) {
	v, ok := collection.Load(namespacedName)
	if !ok {
		return nil, ok
	}
	p, ok := v.(*FreeIPAPKI)
	return p, ok
}

// Store adds a new provisioner to the collection by NamespacedName.
func Store(namespacedName types.NamespacedName, provisioner *FreeIPAPKI) {
	collection.Store(namespacedName, provisioner)
}

type CertPem []byte
type CaPem []byte

const certKey = "certificate"

// Sign sends the certificate requests to the CA and returns the signed
// certificate.
func (s *FreeIPAPKI) Sign(ctx context.Context, cr *certmanager.CertificateRequest) (CertPem, CaPem, error) {
	csr, err := pki.DecodeX509CertificateRequestBytes(cr.Spec.Request)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode CSR for signing: %s", err)
	}

	if csr.Subject.CommonName == "" {
		return nil, nil, fmt.Errorf("Request has no common name")
	}

	// Adding Host
	if s.spec.AddHost {
		if _, err := s.client.HostShow(&freeipa.HostShowArgs{Fqdn: csr.Subject.CommonName}, &freeipa.HostShowOptionalArgs{}); err != nil {
			if ipaE, ok := err.(*freeipa.Error); ok && ipaE.Code == freeipa.NotFoundCode {
				if _, err := s.client.HostAdd(&freeipa.HostAddArgs{
					Fqdn: csr.Subject.CommonName,
				}, &freeipa.HostAddOptionalArgs{
					Force: freeipa.Bool(true),
				}); err != nil {
					return nil, nil, fmt.Errorf("fail adding host: %v", err)
				}
			} else {
				return nil, nil, fmt.Errorf("fail getting Host wi: %v", err)
			}
		}
	}

	name := fmt.Sprintf("%s/%s", s.spec.ServiceName, csr.Subject.CommonName)

	// Adding service
	if s.spec.AddService {
		svcList, err := s.client.ServiceFind(
			name,
			&freeipa.ServiceFindArgs{},
			&freeipa.ServiceFindOptionalArgs{
				PkeyOnly:  freeipa.Bool(true),
				Sizelimit: freeipa.Int(1),
			})

		if err != nil {
			if !s.spec.IgnoreError {
				return nil, nil, fmt.Errorf("fail listing services: %v", err)
			}
			s.client.ServiceAdd(&freeipa.ServiceAddArgs{Krbcanonicalname: name}, &freeipa.ServiceAddOptionalArgs{Force: freeipa.Bool(true)})
		} else if svcList.Count == 0 {
			if _, err := s.client.ServiceAdd(&freeipa.ServiceAddArgs{Krbcanonicalname: name}, &freeipa.ServiceAddOptionalArgs{Force: freeipa.Bool(true)}); err != nil {
				return nil, nil, fmt.Errorf("fail adding service: %v", err)
			}
		}
	}

	result, err := s.client.CertRequest(&freeipa.CertRequestArgs{
		Csr:       string(cr.Spec.Request),
		Principal: name,
	}, &freeipa.CertRequestOptionalArgs{
		Cacn: &s.spec.Ca,
		Add:  &s.spec.AddPrincipal,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Fail to request certificate: %v", err)
	}

	reqCertShow := &freeipa.CertShowArgs{
		SerialNumber: result.Value,
	}

	var certPem string
	var caPem string

	cert, err := s.client.CertShow(reqCertShow, &freeipa.CertShowOptionalArgs{})
	if err != nil || len(*cert.Result.CertificateChain) < 2 {
		// Try fallback with parsing result
		certPem, ok := result.Result.(map[string]interface{})[certKey].(string)

		if !ok || certPem == "" {
			return nil, nil, fmt.Errorf("can't find certificate for: %s", result.String())
		}
	} else {
		certPem = (*cert.Result.CertificateChain)[0]
		caPem = (*cert.Result.CertificateChain)[1]
	}

	certPem = fmt.Sprintf("-----BEGIN CERTIFICATE-----\n%s\n-----END CERTIFICATE-----", certPem)
	if caPem != "" {
		caPem = fmt.Sprintf("-----BEGIN CERTIFICATE-----\n%s\n-----END CERTIFICATE-----", caPem)
	}

	return []byte(certPem), []byte(caPem), nil
}
