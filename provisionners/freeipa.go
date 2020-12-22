package provisioners

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"

	api "github.com/guilhem/freeipa-issuer/api/v1beta1"
	certmanager "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1alpha2"
	"github.com/jetstack/cert-manager/pkg/util/pki"
	"github.com/tehwalris/go-freeipa/freeipa"
	"k8s.io/apimachinery/pkg/types"
)

var collection = new(sync.Map)

// FreeIPAPKI
type FreeIPAPKI struct {
	name   string
	client *freeipa.Client
}

// New returns a new Step provisioner, configured with the information in the
// given issuer.
func New(iss *api.Issuer, user, password string) (*FreeIPAPKI, error) {
	tspt := http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // WARNING DO NOT USE THIS OPTION IN PRODUCTION
		},
	}

	client, err := freeipa.Connect(iss.Spec.Host, &tspt, user, password)
	if err != nil {
		return nil, fmt.Errorf("error when connecting: %v", err)
	}

	// if len(iss.Spec.CABundle) > 0 {
	// 	options = append(options, ca.WithCABundle(iss.Spec.CABundle))
	// }

	p := &FreeIPAPKI{
		name:   fmt.Sprintf("%s.%s", iss.Name, iss.Namespace),
		client: client,
	}

	// // Request identity certificate if required.
	// if version, err := provisioner.Version(); err == nil {
	// 	if version.RequireClientAuthentication {
	// 		if err := p.createIdentityCertificate(); err != nil {
	// 			return nil, err
	// 		}
	// 	}
	// }

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

func (s *FreeIPAPKI) createIdentityCertificate() error {
	// csr, pk, err := ca.CreateCertificateRequest(s.name)
	// if err != nil {
	// 	return err
	// }
	// token, err := s.provisioner.Token(s.name)
	// if err != nil {
	// 	return err
	// }
	// resp, err := s.provisioner.Sign(&capi.SignRequest{
	// 	CsrPEM: *csr,
	// 	OTT:    token,
	// })
	// if err != nil {
	// 	return err
	// }
	// tr, err := s.provisioner.Client.Transport(context.Background(), resp, pk)
	// if err != nil {
	// 	return err
	// }
	// s.provisioner.Client.SetTransport(tr)
	return nil
}

type CertPem []byte
type CaPem []byte

const certKey = "certificate"

// Sign sends the certificate requests to the Step CA and returns the signed
// certificate.
func (s *FreeIPAPKI) Sign(ctx context.Context, cr *certmanager.CertificateRequest) (CertPem, CaPem, error) {
	csr, err := pki.DecodeX509CertificateRequestBytes(cr.Spec.CSRPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode CSR for signing: %s", err)
	}

	if csr.Subject.CommonName == "" {
		return nil, nil, fmt.Errorf("Request has no common name")
	}

	if _, err := s.client.HostShow(&freeipa.HostShowArgs{Fqdn: csr.Subject.CommonName}, &freeipa.HostShowOptionalArgs{}); err != nil {
		if _, err := s.client.HostAdd(&freeipa.HostAddArgs{
			Fqdn: csr.Subject.CommonName,
		}, &freeipa.HostAddOptionalArgs{
			Force: freeipa.Bool(true),
		}); err != nil {
			return nil, nil, fmt.Errorf("fail adding host: %v", err)
		}
	}

	name := fmt.Sprintf("%s/%s", "HTTP", csr.Subject.CommonName)

	ignoreError := true

	// Adding service

	realmResult, err := s.client.RealmdomainsShow(&freeipa.RealmdomainsShowArgs{}, &freeipa.RealmdomainsShowOptionalArgs{})
	if err != nil {
		return nil, nil, err
	}

	canonicalName := fmt.Sprintf("%s@%s", name, strings.ToUpper(realmResult.Result.Associateddomain[0]))

	// // ERROR unexpected value for field Subject: <nil> (<nil>)
	// svcList, err := s.client.ServiceFind(
	// 	name,
	// 	&freeipa.ServiceFindArgs{},
	// 	&freeipa.ServiceFindOptionalArgs{
	// 		// PkeyOnly:  freeipa.Bool(true),
	// 		Sizelimit: freeipa.Int(1),
	// 	})

	// if err != nil {
	// 	return nil, nil, fmt.Errorf("fail listing services: %v", err)
	// }

	if _, err := s.client.ServiceShow(&freeipa.ServiceShowArgs{Krbcanonicalname: canonicalName}, &freeipa.ServiceShowOptionalArgs{}); err != nil {
		if _, err := s.client.ServiceAdd(&freeipa.ServiceAddArgs{Krbcanonicalname: canonicalName}, &freeipa.ServiceAddOptionalArgs{Force: freeipa.Bool(true)}); err != nil && !ignoreError {
			return nil, nil, fmt.Errorf("fail adding service: %v", err)
		}
	}

	result, err := s.client.CertRequest(&freeipa.CertRequestArgs{
		Csr:       string(cr.Spec.CSRPEM),
		Principal: name,
	}, &freeipa.CertRequestOptionalArgs{
		Cacn:  freeipa.String("ipa"),
		Add:   freeipa.Bool(true),
		Chain: freeipa.Bool(true),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Fail to request certificate: %v", err)
	}

	// fail with freeipa 4.6.6
	//
	// reqCertShow := &freeipa.CertShowArgs{
	// 	SerialNumber: result.Value,
	// }
	// cert, err := s.client.CertShow(reqCertShow, &freeipa.CertShowOptionalArgs{})
	// if err != nil {
	// 	return nil, nil, fmt.Errorf("fail to download cert: %v", err)
	// }

	dcert, ok := result.Result.(map[string]interface{})[certKey]

	if !ok || dcert == "" {
		return nil, nil, fmt.Errorf("can't find certificate for: %s", result.String())
	}

	cert := fmt.Sprintf("-----BEGIN CERTIFICATE-----\n%s\n-----END CERTIFICATE-----", dcert.(string))
	// caPem := fmt.Sprintf("-----BEGIN CERTIFICATE-----\n%s\n-----END CERTIFICATE-----", (*cert.Result.CertificateChain)[1])

	return []byte(cert), nil, nil
}
