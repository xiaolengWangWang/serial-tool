#ifndef SERIAL_TOOL_APP_H
#define SERIAL_TOOL_APP_H

void RunApp(void);
void UIAppend(const char *text);
void UIMonitorAppend(const char *text);
void UIConnectionClosed(void);

char *GoListPorts(void);
char *GoConnect(char *name, int baud, int dataBits, int stopBits, char *parity, int hexView);
char *GoListen(char *address, int hexView);
char *GoConnectTCP(char *address, int hexView);
char *GoListenUDP(char *address, int hexView);
char *GoConnectUDP(char *address, int hexView);
char *GoStartSerialServer(char *serialName, int baud, int dataBits, int stopBits, char *parity,
                          char *protocol, char *role, char *address, int hexView);
char *GoConnectHTTP(char *url);
char *GoHTTPRequest(char *path);
char *GoAddVSerial(char *address);
void GoRemoveVSerial(int id);
void GoDisconnect(void);
char *GoSend(char *text, int hexMode, char *eol);
char *GoNetworkSend(char *text, int hexMode, char *eol);
char *GoUDPSend(char *text, int hexMode, char *eol);
void GoSetHexView(int enabled);
char *GoDatabaseInfo(void);
char *GoRecentSessions(void);
char *GoLocalIP(void);
char *GoLocalIPs(void);

#endif
