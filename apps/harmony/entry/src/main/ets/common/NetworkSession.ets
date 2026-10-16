import { socket } from '@kit.NetworkKit';

export type NetworkMode = 'TCP' | 'UDP';

export class NetworkSession {
  private tcp?: socket.TCPSocket;
  private udp?: socket.UDPSocket;
  private target?: socket.NetAddress;
  private generation: number = 0;
  private readonly onData: (bytes: Uint8Array) => void;
  private readonly onStatus: (status: string) => void;

  constructor(
    onData: (bytes: Uint8Array) => void,
    onStatus: (status: string) => void
  ) {
    this.onData = onData;
    this.onStatus = onStatus;
  }

  async connect(mode: NetworkMode, host: string, port: number): Promise<void> {
    await this.close();
    const generation = ++this.generation;
    const target: socket.NetAddress = { address: host, port: port };
    if (mode === 'TCP') {
      const tcp = socket.constructTCPSocketInstance();
      this.tcp = tcp;
      tcp.on('message', (info: socket.SocketMessageInfo) => {
        if (generation === this.generation) this.onData(new Uint8Array(info.message));
      });
      tcp.on('close', () => {
        if (generation === this.generation) this.onStatus('连接已关闭');
      });
      await tcp.bind({ address: '0.0.0.0', port: 0 });
      await tcp.connect({ address: target, timeout: 6000 });
    } else {
      const udp = socket.constructUDPSocketInstance();
      this.udp = udp;
      this.target = target;
      udp.on('message', (info: socket.SocketMessageInfo) => {
        if (generation === this.generation) this.onData(new Uint8Array(info.message));
      });
      await udp.bind({ address: '0.0.0.0', port: 0 });
    }
    this.onStatus(mode + ' 已连接');
  }

  async send(bytes: Uint8Array): Promise<void> {
    const data = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
    if (this.tcp) {
      await this.tcp.send({ data: data });
    } else if (this.udp && this.target) {
      await this.udp.send({ data: data, address: this.target });
    } else {
      throw new Error('请先连接');
    }
  }

  async close(): Promise<void> {
    ++this.generation;
    const tcp = this.tcp;
    const udp = this.udp;
    this.tcp = undefined;
    this.udp = undefined;
    this.target = undefined;
    if (tcp) {
      tcp.off('message');
      tcp.off('close');
      await tcp.close();
    }
    if (udp) {
      udp.off('message');
      await udp.close();
    }
  }
}
