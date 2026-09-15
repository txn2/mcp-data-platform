package main

import (
	"fmt"
	"strings"
)

// wsdl renders this service's description.
//
// It is assembled here rather than held as a file so the two versions differ
// in exactly the ways they are meant to — the binding namespace, the transport
// media type and elementFormDefault — and in no others. A pair of hand-kept
// files would drift, and the difference between them is the whole point.
func (s service) wsdl() string {
	soapNS := "http://schemas.xmlsoap.org/wsdl/soap/"
	prefix := "soap"
	if s.version == "1.2" {
		soapNS = "http://schemas.xmlsoap.org/wsdl/soap12/"
	}
	form := ""
	if s.qualified {
		form = ` elementFormDefault="qualified"`
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, `<?xml version="1.0" encoding="utf-8"?>
<wsdl:definitions targetNamespace=%q
  xmlns:tns=%q
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:%s=%q
  xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/">
  <wsdl:documentation>Orders, for exercising the platform's WSDL import.</wsdl:documentation>
  <wsdl:types>
    <xsd:schema targetNamespace=%q%s>
      <xsd:element name="GetOrder">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="OrderId" type="xsd:string"/>
            <xsd:element name="Detail" type="xsd:boolean" minOccurs="0"/>
          </xsd:sequence>
          <xsd:attribute name="RequestId" type="xsd:string"/>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="GetOrderResponse">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="OrderId" type="xsd:string"/>
          <xsd:element name="Total" type="xsd:decimal"/>
          <xsd:element name="Status" type="xsd:string"/>
          <xsd:element name="Detailed" type="xsd:string" minOccurs="0"/>
          <xsd:element name="ReceivedAttribute" type="xsd:string" minOccurs="0"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="ListOrders">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="Customer" type="xsd:string"/>
          <xsd:element name="Limit" type="xsd:int" minOccurs="0"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="ListOrdersResponse">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="Customer" type="xsd:string"/>
          <xsd:element name="Order" type="tns:Order" minOccurs="0" maxOccurs="unbounded"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:complexType name="Order">
        <xsd:sequence>
          <xsd:element name="Id" type="xsd:string"/>
          <xsd:element name="Total" type="xsd:decimal"/>
        </xsd:sequence>
      </xsd:complexType>
      <xsd:element name="FailOrder">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="Reason" type="xsd:string"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </wsdl:types>
  <wsdl:message name="GetOrderIn"><wsdl:part name="parameters" element="tns:GetOrder"/></wsdl:message>
  <wsdl:message name="GetOrderOut"><wsdl:part name="parameters" element="tns:GetOrderResponse"/></wsdl:message>
  <wsdl:message name="ListOrdersIn"><wsdl:part name="parameters" element="tns:ListOrders"/></wsdl:message>
  <wsdl:message name="ListOrdersOut"><wsdl:part name="parameters" element="tns:ListOrdersResponse"/></wsdl:message>
  <wsdl:message name="FailOrderIn"><wsdl:part name="parameters" element="tns:FailOrder"/></wsdl:message>
  <wsdl:portType name="OrdersSoap">
    <wsdl:operation name="GetOrder">
      <wsdl:documentation>Returns one order. Echoes the fields it received.</wsdl:documentation>
      <wsdl:input message="tns:GetOrderIn"/>
      <wsdl:output message="tns:GetOrderOut"/>
    </wsdl:operation>
    <wsdl:operation name="ListOrders">
      <wsdl:documentation>Returns a customer's orders as a repeated element.</wsdl:documentation>
      <wsdl:input message="tns:ListOrdersIn"/>
      <wsdl:output message="tns:ListOrdersOut"/>
    </wsdl:operation>
    <wsdl:operation name="FailOrder">
      <wsdl:documentation>Always answers with a soap:Fault.</wsdl:documentation>
      <wsdl:input message="tns:FailOrderIn"/>
    </wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="OrdersBinding" type="tns:OrdersSoap">
    <%s:binding transport="http://schemas.xmlsoap.org/soap/http" style="document"/>
`, targetNS, targetNS, prefix, soapNS, targetNS, form, prefix)

	for _, op := range []string{"GetOrder", "ListOrders", "FailOrder"} {
		_, _ = fmt.Fprintf(&b, `    <wsdl:operation name="%s">
      <%s:operation soapAction="%s/%s"/>
      <wsdl:input><%s:body use="literal"/></wsdl:input>
      <wsdl:output><%s:body use="literal"/></wsdl:output>
    </wsdl:operation>
`, op, prefix, targetNS, op, prefix, prefix)
	}

	_, _ = fmt.Fprintf(&b, `  </wsdl:binding>
  <wsdl:service name="OrdersService">
    <wsdl:documentation>The dev stack's SOAP upstream.</wsdl:documentation>
    <wsdl:port name="OrdersSoap" binding="tns:OrdersBinding">
      <%s:address location="http://localhost%s%s"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>
`, prefix, advertised, s.path)
	return b.String()
}

// advertised is the address the service descriptions announce, set from the
// listen address at startup. Only the PATH of it reaches a catalog spec — a
// shared spec carries no host, and each connection supplies its own base_url —
// but a WSDL that advertised no address at all would not be importable.
var advertised = defaultAddr
