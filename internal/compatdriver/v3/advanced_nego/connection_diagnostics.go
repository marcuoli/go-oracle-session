package advanced_nego

// ServiceDiagnostics contains only allowlisted algorithm names and versions
// actually received from the server. "none" means a received algorithm ID of
// zero; a missing response must not be reported as disabled native security.
type ServiceDiagnostics struct {
	Algorithm      string
	AlgorithmKnown bool
	Version        uint32
	VersionKnown   bool
}

type ConnectionDiagnostics struct {
	Encryption ServiceDiagnostics
	Integrity  ServiceDiagnostics
}

func serviceDiagnostics(serv *defaultService, id int, algorithmKnown, versionKnown bool) ServiceDiagnostics {
	d := ServiceDiagnostics{VersionKnown: versionKnown, AlgorithmKnown: algorithmKnown}
	if versionKnown {
		d.Version = serv.version
	}
	if !algorithmKnown {
		return d
	}
	d.Algorithm = "unknown"
	for i, knownID := range serv.availableServiceIDs {
		if knownID == id {
			d.Algorithm = serv.availableServiceNames[i]
			if id == 0 {
				d.Algorithm = "none"
			}
			break
		}
	}
	return d
}

// ConnectionDiagnostics returns scalar negotiation observations without raw
// authentication values, network packets or cryptographic material.
func (nego *AdvNego) ConnectionDiagnostics() ConnectionDiagnostics {
	var d ConnectionDiagnostics
	if serv, ok := nego.serviceList[2].(*encryptService); ok {
		d.Encryption = serviceDiagnostics(&serv.defaultService, serv.algoID, serv.algorithmReceived, serv.versionReceived)
	}
	if serv, ok := nego.serviceList[3].(*dataIntegrityService); ok {
		d.Integrity = serviceDiagnostics(&serv.defaultService, serv.algoID, serv.algorithmReceived, serv.versionReceived)
	}
	return d
}
