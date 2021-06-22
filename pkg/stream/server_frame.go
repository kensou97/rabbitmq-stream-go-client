package stream

import (
	"bufio"
	"bytes"
	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/amqp"
	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/logs"
	"io"
)

type ReaderProtocol struct {
	FrameLen          uint32
	CommandID         uint16
	Key               uint16
	Version           uint16
	CorrelationId     uint32
	ResponseCode      uint16
	PublishID         uint8
	PublishingIdCount uint64
}

func (c *Client) handleResponse() {
	buffer := bufio.NewReader(c.socket.connection)
	for {
		readerProtocol := &ReaderProtocol{}
		frameLen, err := readUInt(buffer)
		if err != nil {
			logs.LogDebug("socket error: %s", err)
			_ = c.Close()
			break
		}
		readerProtocol.FrameLen = frameLen
		readerProtocol.CommandID = uShortExtractResponseCode(readUShort(buffer))
		readerProtocol.Version = readUShort(buffer)

		switch readerProtocol.CommandID {

		case commandPeerProperties:
			{
				c.handlePeerProperties(readerProtocol, buffer)
			}
		case commandSaslHandshake:
			{
				c.handleSaslHandshakeResponse(readerProtocol, buffer)
			}
		case commandTune:
			{
				c.handleTune(buffer)
			}
		case commandDeclarePublisher,
			CommandDeletePublisher, commandDeleteStream,
			commandCreateStream, commandSaslAuthenticate, commandSubscribe,
			CommandUnsubscribe:
			{
				c.handleGenericResponse(readerProtocol, buffer)
			}
		case commandOpen:
			{
				c.commandOpen(readerProtocol, buffer)
			}
		case commandPublishError:
			{
				c.handlePublishError(buffer)

			}
		case commandPublishConfirm:
			{
				c.handleConfirm(readerProtocol, buffer)
			}
		case commandDeliver:
			{
				c.handleDeliver(buffer)

			}
		case commandQueryPublisherSequence:
			{
				c.queryPublisherSequenceFrameHandler(readerProtocol, buffer)
			}
		case CommandMetadataUpdate:
			{

				c.metadataUpdateFrameHandler(buffer)
			}
		case commandCredit:
			{
				c.creditNotificationFrameHandler(readerProtocol, buffer)
			}
		case commandHeartbeat:
			{

				//logDebug("RECEIVED Heartbeat %d buff:%d \n", readerProtocol.CommandID, buffer.Buffered())

			}
		case CommandQueryOffset:
			{
				c.queryOffsetFrameHandler(readerProtocol, buffer)

			}
		case commandMetadata:
			{
				c.metadataFrameHandler(readerProtocol, buffer)
			}
		case CommandClose:
			{
				c.closeFrameHandler(readerProtocol, buffer)
			}
		default:
			{
				logs.LogWarn("Command not implemented %d buff:%d \n", readerProtocol.CommandID, buffer.Buffered())
				break
			}
		}
	}

}

func (c *Client) handleSaslHandshakeResponse(streamingRes *ReaderProtocol, r *bufio.Reader) interface{} {
	streamingRes.CorrelationId, _ = readUInt(r)
	streamingRes.ResponseCode = uShortExtractResponseCode(readUShort(r))
	mechanismsCount, _ := readUInt(r)
	var mechanisms []string
	for i := 0; i < int(mechanismsCount); i++ {
		mechanism := readString(r)
		mechanisms = append(mechanisms, mechanism)
	}

	res, err := c.coordinator.GetResponseById(streamingRes.CorrelationId)
	if err != nil {
		// TODO handle response
		return err
	}
	res.data <- mechanisms

	return mechanisms
}

func (c *Client) handlePeerProperties(readProtocol *ReaderProtocol, r *bufio.Reader) {
	readProtocol.CorrelationId, _ = readUInt(r)
	readProtocol.ResponseCode = uShortExtractResponseCode(readUShort(r))
	serverPropertiesCount, _ := readUInt(r)
	serverProperties := make(map[string]string)

	for i := 0; i < int(serverPropertiesCount); i++ {
		key := readString(r)
		value := readString(r)
		serverProperties[key] = value
	}
	res, err := c.coordinator.GetResponseById(readProtocol.CorrelationId)
	if err != nil {
		// TODO handle response
		return
	}
	res.code <- Code{id: readProtocol.ResponseCode}

}

func (c *Client) handleTune(r *bufio.Reader) interface{} {

	serverMaxFrameSize, _ := readUInt(r)
	serverHeartbeat, _ := readUInt(r)

	maxFrameSize := serverMaxFrameSize
	heartbeat := serverHeartbeat

	length := 2 + 2 + 4 + 4
	var b = bytes.NewBuffer(make([]byte, 0, length+4))
	writeInt(b, length)
	writeUShort(b, uShortEncodeResponseCode(commandTune))
	writeShort(b, version1)
	writeUInt(b, maxFrameSize)
	writeUInt(b, heartbeat)
	res, err := c.coordinator.GetResponseByName("tune")
	if err != nil {
		// TODO handle response
		return err
	}
	res.data <- b.Bytes()
	return b.Bytes()

}

func (c *Client) handleGenericResponse(readProtocol *ReaderProtocol, r *bufio.Reader) {
	readProtocol.CorrelationId, _ = readUInt(r)
	readProtocol.ResponseCode = uShortExtractResponseCode(readUShort(r))
	res, err := c.coordinator.GetResponseById(readProtocol.CorrelationId)
	if err != nil {
		// TODO handle readProtocol
		return
	}
	res.code <- Code{id: readProtocol.ResponseCode}
}

func (c *Client) commandOpen(readProtocol *ReaderProtocol, r *bufio.Reader) {
	readProtocol.CorrelationId, _ = readUInt(r)
	readProtocol.ResponseCode = uShortExtractResponseCode(readUShort(r))
	clientProperties := ClientProperties{
		items: map[string]string{},
	}
	connectionPropertiesCount, _ := readUInt(r)
	for i := 0; i < int(connectionPropertiesCount); i++ {
		clientProperties.items[readString(r)] = readString(r)
	}

	res, err := c.coordinator.GetResponseById(readProtocol.CorrelationId)
	if err != nil {
		// TODO handle readProtocol
		return
	}
	res.code <- Code{id: readProtocol.ResponseCode}
	res.data <- clientProperties

}

func (c *Client) handleConfirm(readProtocol *ReaderProtocol, r *bufio.Reader) interface{} {

	readProtocol.PublishID = readByte(r)
	//readProtocol.PublishingIdCount = ReadIntFromReader(testEnvironment.reader)
	publishingIdCount, _ := readUInt(r)
	//var _publishingId int64
	producer, err := c.coordinator.GetProducerById(readProtocol.PublishID)
	if err != nil {
		logs.LogWarn("can't find the producer during confirmation: %s", err)
		return nil
	}
	var unConfirmed []*UnConfirmedMessage
	for publishingIdCount != 0 {
		m := producer.getUnConfirmed(readInt64(r))
		if m != nil {
			m.Confirmed = true
			unConfirmed = append(unConfirmed, m)
		}
		publishingIdCount--
	}

	producer.mutex.Lock()
	if producer.publishConfirm != nil {
		producer.publishConfirm <- unConfirmed
	}
	producer.mutex.Unlock()
	for _, l := range unConfirmed {
		if l != nil {
			producer.removeUnConfirmed(l.MessageID)
		}
	}
	return 0
}

func (c *Client) queryPublisherSequenceFrameHandler(readProtocol *ReaderProtocol, r *bufio.Reader) {

	readProtocol.CorrelationId, _ = readUInt(r)
	readProtocol.ResponseCode = uShortExtractResponseCode(readUShort(r))
	sequence := readInt64(r)
	res, err := c.coordinator.GetResponseById(readProtocol.CorrelationId)
	if err != nil {
		// TODO handle readProtocol
		return
	}
	res.code <- Code{id: readProtocol.ResponseCode}
	res.data <- sequence
}

func (c *Client) handleDeliver(r *bufio.Reader) {

	subscriptionId := readByte(r)
	consumer, _ := c.coordinator.GetConsumerById(subscriptionId)

	_ = readByte(r)
	chunkType := readByte(r)
	if chunkType != 0 {
		logs.LogWarn("Invalid chunkType: %d ", chunkType)
	}

	_ = readUShort(r)
	numRecords, _ := readUInt(r)
	_ = readInt64(r) // timestamp
	_ = readInt64(r) // epoch, unsigned long
	offset := readInt64(r)
	_, _ = readUInt(r)
	_, _ = readUInt(r)
	_, _ = readUInt(r)
	_, _ = readUInt(r)

	// crc

	//try {
	//	// TODO handle exception in exception handler
	//	chunkChecksum.checksum(message, dataLength, crc);
	//} catch (ChunkChecksumValidationException e) {
	//	LOGGER.warn(
	//		"Checksum failure at offset {}, expecting {}, got {}",
	//		offset,
	//		e.getExpected(),
	//		e.getComputed());
	//	throw e;
	//}

	//fmt.Printf("%d - %d - %d - %d - %d - %d - %d - %d - %d - %d - %d \n", subscriptionId, b, chunkType,
	//		numEntries, numRecords, timestamp, epoch, unsigned, crc, dataLength, trailer)
	//fmt.Printf("%d numRecords %d \n", offset, numRecords)
	c.credit(subscriptionId, 1)

	var offsetLimit int64 = -1

	if consumer.options.Offset.isOffset() {
		offsetLimit = consumer.GetOffset()
	}

	filter := offsetLimit != -1

	//messages
	var batchConsumingMessages []*amqp.Message
	for numRecords != 0 {
		entryType, err := peekByte(r)
		if err != nil {
			if err == io.EOF {
				logs.LogDebug("EOF reading entryType %s ", err)
				return
			} else {
				logs.LogWarn("error reading entryType %s ", err)
			}
		}
		if (entryType & 0x80) == 0 {
			sizeMessage, _ := readUInt(r)

			arrayMessage := readUint8Array(r, sizeMessage)
			if filter && (offset < offsetLimit) {
				/// TODO set recordset as filtered
			} else {
				msg := &amqp.Message{}
				err := msg.UnmarshalBinary(arrayMessage)
				if err != nil {
					logs.LogError("error unmarshal messages: %s", err)
				}
				batchConsumingMessages = append(batchConsumingMessages, msg)
			}

		} else {
			logs.LogWarn("entryType Not Handled %d", entryType)
		}
		numRecords--
		offset++
	}

	consumer.response.data <- offset
	consumer.response.messages <- batchConsumingMessages

}

func (c *Client) creditNotificationFrameHandler(readProtocol *ReaderProtocol, r *bufio.Reader) {
	readProtocol.ResponseCode = uShortExtractResponseCode(readUShort(r))
	//subscriptionId := readByte(r)
	_ = readByte(r)
	// TODO ASK WHAT TO DO HERE
}

func (c *Client) queryOffsetFrameHandler(readProtocol *ReaderProtocol, r *bufio.Reader) {
	c.handleGenericResponse(readProtocol, r)
	offset := readInt64(r)
	res, err := c.coordinator.GetResponseById(readProtocol.CorrelationId)
	if err != nil {
		// TODO handle readProtocol
		return
	}
	res.data <- offset
}

func (c *Client) handlePublishError(buffer *bufio.Reader) {

	publisherId := readByte(buffer)
	producer, err := c.coordinator.GetProducerById(publisherId)
	if err != nil {
		logs.LogWarn("producer not found :%s", err)
		producer = &Producer{unConfirmedMessages: map[int64]*UnConfirmedMessage{}}
	}

	publishingErrorCount, _ := readUInt(buffer)
	var publishingId int64
	var code uint16
	for publishingErrorCount != 0 {
		publishingId = readInt64(buffer)
		code = readUShort(buffer)

		if producer.publishError != nil {
			producer.publishError <- PublishError{
				Code:               code,
				Err:                lookErrorCode(code),
				UnConfirmedMessage: producer.getUnConfirmed(publishingId),
			}
		}
		producer.removeUnConfirmed(publishingId)
		publishingErrorCount--
	}

}

func (c *Client) metadataUpdateFrameHandler(buffer *bufio.Reader) {
	code := readUShort(buffer)
	if code == responseCodeStreamNotAvailable {
		stream := readString(buffer)
		logs.LogDebug("stream %s is no longer available", stream)
		c.mutex.Lock()
		c.metadataListener <- metaDataUpdateEvent{
			StreamName: stream,
			code:       responseCodeStreamNotAvailable,
		}
		c.mutex.Unlock()

	} else {
		//TODO handle the error, see the java code
		logs.LogWarn("unsupported metadata update code %d", code)
	}
}

func (c *Client) metadataFrameHandler(readProtocol *ReaderProtocol, r *bufio.Reader) {
	readProtocol.CorrelationId, _ = readUInt(r)
	readProtocol.ResponseCode = responseCodeOk
	brokers := newBrokers()
	brokersCount, _ := readUInt(r)
	for i := 0; i < int(brokersCount); i++ {
		brokerReference := readShort(r)
		host := readString(r)
		port, _ := readUInt(r)
		brokers.Add(brokerReference, host, port)
	}

	streamsMetadata := StreamsMetadata{}.New()
	streamsCount, _ := readUInt(r)
	for i := 0; i < int(streamsCount); i++ {
		stream := readString(r)
		responseCode := readUShort(r)
		var leader *Broker
		var replicas []*Broker
		leaderReference := readShort(r)
		leader = brokers.Get(leaderReference)
		replicasCount, _ := readUInt(r)
		for i := 0; i < int(replicasCount); i++ {
			replicaReference := readShort(r)
			replicas = append(replicas, brokers.Get(replicaReference))
		}
		streamsMetadata.Add(stream, responseCode, leader, replicas)
	}

	res, err := c.coordinator.GetResponseById(readProtocol.CorrelationId)
	if err != nil {
		// TODO handle readProtocol
		return
	}
	res.code <- Code{id: readProtocol.ResponseCode}
	res.data <- streamsMetadata
}

func (c *Client) closeFrameHandler(readProtocol *ReaderProtocol, r *bufio.Reader) {
	readProtocol.CorrelationId, _ = readUInt(r)
	readProtocol.ResponseCode = uShortExtractResponseCode(readUShort(r))
	closeReason := readString(r)
	logs.LogInfo("Received close from server, reason: {} {}", lookErrorCode(readProtocol.ResponseCode),
		closeReason)

	length := 2 + 2 + 4 + 2
	var b = bytes.NewBuffer(make([]byte, 0, length))
	writeProtocolHeader(b, length, int16(uShortEncodeResponseCode(CommandClose)),
		int(readProtocol.CorrelationId))
	writeUShort(b, responseCodeOk)

	err := c.socket.writeAndFlush(b.Bytes())
	if err != nil {
		return
	}

}