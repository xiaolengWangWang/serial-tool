#ifndef SERIAL_TOOL_APP_H
#define SERIAL_TOOL_APP_H

void RunApp(void);
void UIAppend(const char *text);
void UIAppendLog(const char *text);
void UIMonitorAppend(const char *text);
void UIConnectionClosed(void);
void UIAddPacket(const char *ts, const char *dir, const char *hex, const char *ascii, int len);

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
char *GoListVSerialLinks(void);
void GoDisconnect(void);
char *GoSend(char *text, int hexMode, char *eol);
char *GoNetworkSend(char *text, int hexMode, char *eol);
char *GoUDPSend(char *text, int hexMode, char *eol);
void GoSetHexView(int enabled);
char *GoDatabaseInfo(void);
char *GoVersion(void);
char *GoStats(void);
char *GoFavoriteNames(void);
char *GoSaveFavorite(char *name, char *value);
void GoDeleteFavorite(char *name);
char *GoFavorite(char *name);
char *GoRecentSends(void);
char *GoChecksum(char *kind, char *input);
char *GoRecentSessions(void);
char *GoLocalIP(void);
char *GoLocalIPs(void);

#endif
