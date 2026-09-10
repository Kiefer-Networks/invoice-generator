import java.io.File;
import javax.xml.XMLConstants;
import javax.xml.parsers.DocumentBuilderFactory;
import javax.xml.validation.SchemaFactory;
import javax.xml.transform.dom.DOMSource;
class Validate {
 public static void main(String[] args) throws Exception {
  var factory=DocumentBuilderFactory.newInstance();
  factory.setNamespaceAware(true);
  factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl",true);
  factory.setFeature("http://xml.org/sax/features/external-general-entities",false);
  factory.setFeature("http://xml.org/sax/features/external-parameter-entities",false);
  factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_DTD,"");
  factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_SCHEMA,"");
  var doc=factory.newDocumentBuilder().parse(new File(args[1]));
  var sf=SchemaFactory.newInstance(XMLConstants.W3C_XML_SCHEMA_NS_URI);
  sf.setFeature(XMLConstants.FEATURE_SECURE_PROCESSING,true);
  sf.setProperty(XMLConstants.ACCESS_EXTERNAL_DTD,"");
  sf.setProperty(XMLConstants.ACCESS_EXTERNAL_SCHEMA,"file");
  var validator=sf.newSchema(new File(args[0])).newValidator();
  validator.setProperty(XMLConstants.ACCESS_EXTERNAL_DTD,"");
  validator.setProperty(XMLConstants.ACCESS_EXTERNAL_SCHEMA,"");
  validator.validate(new DOMSource(doc));
 }
}
