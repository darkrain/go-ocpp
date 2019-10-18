package cs

import (
	"errors"
	"net/http"
	"strings"

	"github.com/eduhenke/go-ocpp"
	"github.com/eduhenke/go-ocpp/internal/log"
	"github.com/eduhenke/go-ocpp/internal/service"
	"github.com/eduhenke/go-ocpp/messages"
	"github.com/eduhenke/go-ocpp/messages/v1x/cpreq"
	"github.com/eduhenke/go-ocpp/messages/v1x/cpresp"
	"github.com/eduhenke/go-ocpp/soap"
	"github.com/eduhenke/go-ocpp/ws"
)

// ChargePointMessageHandler handles the OCPP messages coming from the charger
type ChargePointMessageHandler func(cprequest cpreq.ChargePointRequest, cpID string) (cpresp.ChargePointResponse, error)

type CentralSystem interface {
	Run(port string, cphandler ChargePointMessageHandler) error
	GetServiceOf(cpID string, version ocpp.Version, url string) (service.ChargePoint, error) 
}

type centralSystem struct {
	conns map[string]*ws.Conn
}

func New() CentralSystem {
	return &centralSystem{
		conns: make(map[string]*ws.Conn, 0),
	}
}


func (csys *centralSystem) Run(port string, cphandler ChargePointMessageHandler) error {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if ws.IsWebSocketUpgrade(r) {
			csys.handleWebsocket(w, r, cphandler)
		} else {
			csys.handleSoap(w, r, cphandler)
		}

	})
	log.Debug("Central system running on port: %s\n", port)
	return http.ListenAndServe(port, nil)
}

func (csys *centralSystem) handleWebsocket(w http.ResponseWriter, r *http.Request, cphandler ChargePointMessageHandler) {
	log.Debug("New WS-wannabe request\n")
	log.Debug("Current WS connections map: %v\n", csys.conns)
	cpID := strings.TrimPrefix(r.URL.Path, "/")
	conn, err := ws.Handshake(w, r, []ocpp.Version{ocpp.V16})
	if err != nil {
		log.Error("Couldn't handshake request %w\n", err)
		return
	}
	csys.conns[cpID] = conn
	log.Debug("Accepted new WS conn: %v. from: %v\n", conn, cpID)
	log.Debug("Current WS connections map: %v\n", csys.conns)
	go func() {
		for {
			err := conn.ReadMessage()
			if err != nil {
				log.Error("On receiving a message: %w\n", err)
				_ = conn.Close()
				delete(csys.conns, cpID)
				break
			}
		}
	}()
	for {
		req := <-conn.Requests()
		cprequest, ok := req.Request.(cpreq.ChargePointRequest)
		if !ok {
			log.Error(cpreq.ErrorNotChargePointRequest.Error())
		}
		cpresponse, err := cphandler(cprequest, cpID)
		err = conn.SendResponse(req.MessageID, cpresponse, err)
		if err != nil {
			log.Error(err.Error())
		}
	}
}

func (csys *centralSystem) handleSoap(w http.ResponseWriter, r *http.Request, cphandler ChargePointMessageHandler) {
	log.Debug("New SOAP request\n")
	err := soap.Handle(w, r, func(request messages.Request, cpID string) (messages.Response, error) {
		req, ok := request.(cpreq.ChargePointRequest)
		if !ok {
			return nil, errors.New("request is not a cprequest")
		}
		return cphandler(req, cpID)
	})
	if err != nil {
		log.Error("Couldn't handle SOAP request: %w", err)
	}
}

func (csys *centralSystem) GetServiceOf(cpID string, version ocpp.Version, url string) (service.ChargePoint, error) {
	if version == ocpp.V15 {
		return service.NewChargePointSOAP(url, nil), nil
	}
	if version == ocpp.V16 {
		conn := csys.conns[cpID]
		if conn == nil { // TODO: or conn closed
			return nil, errors.New("no connection to this charge point")
		}
		return service.NewChargePointJSON(conn), nil
	}
	return nil, errors.New("charge point has no configured OCPP version(1.5/1.6)")
}
