import { NetworkMode, NetworkSession } from '../common/NetworkSession';
import { TextEncoding, decodeText, encodeText } from '../common/TextCodec';
import { formatHex, parseHex } from '../common/PacketCodec';

@Entry
@Component
struct Index {
  @State private mode: NetworkMode = 'TCP';
  @State private host: string = '';
  @State private port: string = '502';
  @State private connected: boolean = false;
  @State private status: string = '未连接';
  @State private encoding: TextEncoding = 'UTF-8';
  @State private hexMode: boolean = false;
  @State private sendText: string = '';
  @State private records: string[] = [];
  private session?: NetworkSession;

  aboutToAppear(): void {
    this.session = new NetworkSession(
      (bytes: Uint8Array) => this.addRecord('接收', bytes),
      (status: string) => {
        this.status = status;
        if (status === '连接已关闭') this.connected = false;
      }
    );
  }

  aboutToDisappear(): void {
    this.session?.close();
  }

  private addRecord(direction: string, bytes: Uint8Array): void {
    const payload = this.hexMode ? formatHex(bytes) : decodeText(bytes, this.encoding);
    this.records = [...this.records, direction + ' · ' + bytes.length + ' 字节 · ' + payload].slice(-500);
  }

  private async changeConnection(): Promise<void> {
    if (!this.session) return;
    if (this.connected) {
      try {
        await this.session.close();
        this.connected = false;
        this.status = '已断开';
      } catch (error) {
        this.status = '断开失败：' + String(error);
      }
      return;
    }
    const port = Number(this.port);
    if (!this.host.trim() || !Number.isInteger(port) || port < 1 || port > 65535) {
      this.status = '请输入 IP 地址和 1–65535 端口';
      return;
    }
    this.status = '正在连接…';
    try {
      await this.session.connect(this.mode, this.host.trim(), port);
      this.connected = true;
    } catch (error) {
      this.status = '连接失败：' + String(error);
      this.connected = false;
      await this.session.close();
    }
  }

  private async send(): Promise<void> {
    if (!this.session || !this.connected) return;
    try {
      const bytes = this.hexMode ? parseHex(this.sendText) : encodeText(this.sendText, this.encoding);
      if (bytes.length === 0) {
        this.status = '请输入要发送的内容';
        return;
      }
      await this.session.send(bytes);
      this.addRecord('发送', bytes);
      this.status = '已发送 ' + bytes.length + ' 字节';
    } catch (error) {
      this.status = '发送失败：' + String(error);
    }
  }

  build() {
    Column({ space: 12 }) {
      Text('CommBox').fontSize(25).fontWeight(FontWeight.Bold)
      Text('鸿蒙原生网络收发').fontSize(13).fontColor('#5B6472')

      Row({ space: 8 }) {
        Button('TCP').enabled(!this.connected).onClick(() => this.mode = 'TCP')
        Button('UDP').enabled(!this.connected).onClick(() => this.mode = 'UDP')
        Text(this.status).fontSize(13).maxLines(2)
      }.width('100%')

      TextInput({ text: this.host, placeholder: '设备 IP 地址' })
        .width('100%').enabled(!this.connected)
        .onChange((value: string) => this.host = value)
      TextInput({ text: this.port, placeholder: '端口' })
        .type(InputType.Number).width('100%').enabled(!this.connected)
        .onChange((value: string) => this.port = value)
      Button(this.connected ? '断开' : '连接')
        .width('100%').onClick(() => this.changeConnection())

      Row({ space: 8 }) {
        Button(this.hexMode ? 'HEX 模式' : '文本模式').onClick(() => this.hexMode = !this.hexMode)
        Button(this.encoding).onClick(() => this.encoding = this.encoding === 'UTF-8' ? 'GBK' : 'UTF-8')
      }.width('100%')

      Text('收发记录').fontSize(16).fontWeight(FontWeight.Medium)
      Scroll() {
        Column({ space: 6 }) {
          ForEach(this.records, (record: string, index: number) => {
            Text(record).fontSize(13).width('100%')
          }, (record: string, index: number) => index.toString())
        }.width('100%')
      }.width('100%').layoutWeight(1)

      TextArea({ text: this.sendText, placeholder: this.hexMode ? '输入十六进制字节' : '输入中文或其他文本' })
        .width('100%').height(140)
        .onChange((value: string) => this.sendText = value)
      Button('发送').width('100%').enabled(this.connected).onClick(() => this.send())
    }.width('100%').height('100%').padding(16)
  }
}
