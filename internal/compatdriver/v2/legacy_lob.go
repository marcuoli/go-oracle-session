package go_ora

import "errors"

var errLegacyClobEncoding = errors.New("invalid legacy character LOB encoding")

// decodedReadData expands Oracle9i variable-width CLOB code units after all
// DATA messages have joined. Each unit has a 0, 1 or 2 byte big-endian magnitude;
// treating those length bytes as UTF-16 data corrupts non-ASCII values.
// BLOBs and later server families keep their original bytes.
func (lob *Lob) decodedReadData() ([]byte, error) {
	data := lob.data.Bytes()
	if lob.connection.dBVersion == nil || lob.connection.dBVersion.MajorVersion != 9 || !lob.variableWidthChar() {
		return data, nil
	}
	var output []byte
	for offset := 0; offset < len(data); {
		width := int(data[offset])
		offset++
		if width > 2 || width > len(data)-offset {
			lob.connection.setBad()
			return nil, errLegacyClobEncoding
		}
		var high, low byte
		if width == 1 {
			low = data[offset]
		} else if width == 2 {
			high, low = data[offset], data[offset+1]
		}
		offset += width
		if lob.littleEndianClob() {
			output = append(output, low, high)
		} else {
			output = append(output, high, low)
		}
	}
	return output, nil
}
